package sentinel

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/arkeonetwork/arkeo/common"
	"github.com/arkeonetwork/arkeo/common/cosmos"
	"github.com/arkeonetwork/arkeo/x/arkeo/types"

	"golang.org/x/time/rate"
)

const (
	QueryArkAuth  = "arkauth"
	QueryContract = "arkcontract"
)

// Create a map to hold the rate limiters for each visitor and a mutex.
var (
	visitors = make(map[string]*rate.Limiter)
	mu       sync.Mutex
)

type ContractAuth struct {
	ContractId uint64
	Timestamp  int64
	Signature  []byte
}

type ArkAuth struct {
	ContractId uint64
	Spender    common.PubKey
	Nonce      int64
	Signature  []byte
}

// String implement fmt.Stringer
func (aa ArkAuth) String() string {
	return GenerateArkAuthString(aa.ContractId, aa.Nonce, aa.Signature)
}

func GenerateArkAuthString(contractId uint64, nonce int64, signature []byte) string {
	return fmt.Sprintf("%s:%s", GenerateMessageToSign(contractId, nonce), hex.EncodeToString(signature))
}

func GenerateMessageToSign(contractId uint64, nonce int64) string {
	return fmt.Sprintf("%d:%d", contractId, nonce)
}

func parseContractAuth(raw string) (ContractAuth, error) {
	var auth ContractAuth
	var err error

	parts := strings.SplitN(raw, ":", 3)

	if len(parts) > 0 {
		auth.ContractId, err = strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return auth, err
		}
	}

	if len(parts) > 0 {
		auth.Timestamp, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return auth, err
		}
	}

	if len(parts) > 2 {
		auth.Signature, err = hex.DecodeString(parts[2])
		if err != nil {
			return auth, err
		}
	}
	return auth, nil
}

func parseArkAuth(raw string) (ArkAuth, error) {
	var aa ArkAuth
	var err error

	parts := strings.SplitN(raw, ":", 3)

	if len(parts) > 0 {
		aa.ContractId, err = strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return aa, err
		}
	}

	if len(parts) > 1 {
		aa.Nonce, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return aa, err
		}
	}

	if len(parts) > 2 {
		aa.Signature, err = hex.DecodeString(parts[2])
		if err != nil {
			return aa, err
		}
	}
	return aa, nil
}

func (aa ArkAuth) Validate(provider common.PubKey) error {
	creator, err := provider.GetMyAddress()
	if err != nil {
		return fmt.Errorf("internal server error: %w", err)
	}
	msg := types.NewMsgClaimContractIncome(creator, aa.ContractId, aa.Nonce, aa.Signature)
	err = msg.ValidateBasic()
	return err
}

func (auth ContractAuth) Validate(lastTimestamp int64, client common.PubKey) error {
	if auth.ContractId == 0 {
		return fmt.Errorf("contract id cannot be zero")
	}
	if auth.Timestamp <= lastTimestamp {
		return fmt.Errorf("timestamp must be larger than %d", lastTimestamp)
	}

	pk, err := cosmos.GetPubKeyFromBech32(cosmos.Bech32PubKeyTypeAccPub, client.String())
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("%d:%d", auth.ContractId, auth.Timestamp)
	if !pk.VerifySignature([]byte(msg), auth.Signature) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

func (auth ContractAuth) String() string {
	sig := hex.EncodeToString(auth.Signature)
	return fmt.Sprintf("Contract Id: %d, Timestamp: %d, Signature: %s", auth.ContractId, auth.Timestamp, sig)
}

func (p Proxy) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		var aa ArkAuth
		args := r.URL.Query()
		raw, aaOK := args[QueryArkAuth]
		if aaOK {
			aa, err = parseArkAuth(raw[0])
			if err != nil {
				p.logger.Error("failed to parse ark auth", "error", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		remoteAddr := p.getRemoteAddr(r)
		contract, err := p.MemStore.Get(strconv.FormatUint(aa.ContractId, 10))
		if err != nil {
			p.logger.Error("failed to fetch contract", "error", err)
		}
		// collect contract configuration
		if contract.Id > 0 {
			conf, err := p.ContractConfigStore.Get(contract.Id)
			if err != nil {
				p.logger.Error("failed to fetch contract configuration", "error", err)
			}
			w = p.enableCORS(w, conf.CORs)

			// enfore IP Whitelist
			if len(conf.WhitelistIPAddresses) > 0 {
				// TODO: using a map would be faster than iterating over a slice
				found := false
				for _, ip := range conf.WhitelistIPAddresses {
					if strings.EqualFold(remoteAddr, ip) {
						found = true
					}
				}
				if !found {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
			}

			if conf.PerUserRateLimit > 0 {
				if ok := p.isRateLimited(contract.Id, remoteAddr, conf.PerUserRateLimit); ok {
					http.Error(w, http.StatusText(429), http.StatusTooManyRequests)
					return
				}
			}
		}

		if err == nil && (contract.Authorization == types.ContractAuthorization_OPEN || aa.Validate(p.Config.ProviderPubKey) == nil) {
			p.logger.Info("serving paid requests", "remote-addr", remoteAddr)
			w.Header().Set("tier", "paid")

			// ensure service of the contract matches first item in the path
			parts := strings.Split(r.URL.Path, "/")
			serviceName := parts[1]
			ser, err := common.NewService(serviceName)
			if err != nil || ser != contract.Service {
				http.Error(w, fmt.Sprintf("contract service doesn't match the serivce name in the path: (%d/%d): %s", ser, contract.Service, err.Error()), http.StatusUnauthorized)
				return
			}

			httpCode, err := p.paidTier(aa, remoteAddr)
			// paidTier can serve the request
			if err == nil {
				next.ServeHTTP(w, r)
				return
			}
			p.logger.Error("failed to serve paid tier request", "error", err, "http_code", httpCode)
		}

		p.logger.Info("serving free tier requests", "remote-addr", remoteAddr)
		w.Header().Set("tier", "free")
		httpCode, err := p.freeTier(remoteAddr)
		if err != nil {
			p.logger.Error("failed to serve free tier request", "error", err)
			http.Error(w, err.Error(), httpCode)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const (
	forwardHeaderName = `X-Forwarded-For`
	xRealIPName       = `X-Real-Ip`
)

func (p Proxy) getRemoteAddr(r *http.Request) string {
	realIP := r.Header.Get(xRealIPName)
	if realIP != "" {
		return realIP
	}
	forwardIP := r.Header.Get(forwardHeaderName)
	if forwardIP != "" {
		return forwardIP
	}
	return r.RemoteAddr
}

func (p Proxy) freeTier(remoteAddr string) (int, error) {
	if ok := p.isRateLimited(0, remoteAddr, p.Config.FreeTierRateLimit); ok {
		return http.StatusTooManyRequests, fmt.Errorf(http.StatusText(429))
	}

	return http.StatusOK, nil
}

func (p Proxy) isRateLimited(contractId uint64, key string, limitTokens int) bool {
	mu.Lock()
	defer mu.Unlock()

	key = fmt.Sprintf("%d-%s", contractId, key)
	limiter, exists := visitors[key]
	if !exists {
		limiter = rate.NewLimiter(rate.Every(time.Minute), limitTokens)
		visitors[key] = limiter
	}

	return !limiter.Allow()
}

func (p Proxy) paidTier(aa ArkAuth, remoteAddr string) (code int, err error) {
	key := strconv.FormatUint(aa.ContractId, 10)
	contract, err := p.MemStore.Get(key)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("internal server error: %w", err)
	}

	if contract.IsExpired(p.MemStore.GetHeight()) {
		return http.StatusPaymentRequired, fmt.Errorf("open a contract")
	}

	sig := hex.EncodeToString(aa.Signature)
	claim := NewClaim(aa.ContractId, aa.Spender, aa.Nonce, sig)
	if p.ClaimStore.Has(key) {
		var err error
		claim, err = p.ClaimStore.Get(key)
		if err != nil {
			return http.StatusInternalServerError, fmt.Errorf("internal server error: %w", err)
		}
		if claim.Nonce >= aa.Nonce {
			return http.StatusBadRequest, fmt.Errorf("bad nonce (%d/%d)", aa.Nonce, claim.Nonce)
		}
	}

	// check if we've exceed the total number of pay-as-you-go queries
	if contract.Type == types.ContractType_PAY_AS_YOU_GO {
		if contract.Deposit.IsNil() || contract.Deposit.LT(cosmos.NewInt(aa.Nonce*contract.Rate.Amount.Int64())) {
			return http.StatusPaymentRequired, fmt.Errorf("contract spent")
		}
	}

	if ok := p.isRateLimited(contract.Id, key, int(contract.QueriesPerMinute)); ok {
		return http.StatusTooManyRequests, fmt.Errorf("client is ratelimited," + http.StatusText(429))
	}

	claim.Nonce = aa.Nonce
	claim.Signature = sig
	claim.Claimed = false
	if err := p.ClaimStore.Set(claim); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("internal server error: %w", err)
	}
	contract.Nonce = aa.Nonce
	p.MemStore.Put(contract)
	return http.StatusOK, nil
}

func (p Proxy) enableCORS(w http.ResponseWriter, cors CORs) http.ResponseWriter {
	if len(cors.AllowOrigins) > 0 {
		w.Header().Set("Access-Control-Allow-Origin", strings.Join(cors.AllowOrigins, ", "))
	}
	if len(cors.AllowMethods) > 0 {
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(cors.AllowMethods, ", "))
	}
	if len(cors.AllowHeaders) > 0 {
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(cors.AllowHeaders, ", "))
	}
	return w
}
