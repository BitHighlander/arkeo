package keeper

import (
	"github.com/arkeonetwork/arkeo/common/cosmos"
	"github.com/arkeonetwork/arkeo/x/arkeo/types"
)

func (k msgServer) EmitBondProviderEvent(ctx cosmos.Context, bond cosmos.Int, msg *types.MsgBondProvider) error {
	return ctx.EventManager().EmitTypedEvent(
		&types.EventBondProvider{
			Provider: msg.Provider,
			Service:  msg.Service,
			BondRel:  msg.Bond,
			BondAbs:  bond,
		},
	)
}

func (k msgServer) EmitCloseContractEvent(ctx cosmos.Context, contract *types.Contract) error {
	return ctx.EventManager().EmitTypedEvent(
		&types.EventCloseContract{
			ContractId: contract.Id,
			Provider:   contract.Provider,
			Service:    contract.Service.String(),
			Client:     contract.Client,
			Delegate:   contract.Delegate,
		},
	)
}

func (k msgServer) EmitModProviderEvent(ctx cosmos.Context, msg *types.MsgModProvider, provider *types.Provider) error {
	return ctx.EventManager().EmitTypedEvent(
		&types.EventModProvider{
			Creator:             msg.Creator,
			Provider:            provider.PubKey,
			Service:             provider.Service.String(),
			MetadataURI:         provider.MetadataUri,
			MetadataNonce:       provider.MetadataNonce,
			Status:              provider.Status,
			MinContractDuration: provider.MinContractDuration,
			MaxContractDuration: provider.MaxContractDuration,
			Rates:               provider.Rates,
			Bond:                provider.Bond,
			SettlementDuration:  provider.SettlementDuration,
		},
	)
}

func (k msgServer) EmitOpenContractEvent(ctx cosmos.Context, openCost int64, contract *types.Contract) error {
	return ctx.EventManager().EmitTypedEvent(
		&types.EventOpenContract{
			Provider:           contract.Provider,
			ContractId:         contract.Id,
			Service:            contract.Service.String(),
			Client:             contract.Client,
			Delegate:           contract.Delegate,
			Options:            contract.Options,
			Height:             contract.Height,
			Duration:           contract.Duration,
			Rate:               contract.Rate,
			OpenCost:           openCost,
			Deposit:            contract.Deposit,
			SettlementDuration: contract.SettlementDuration,
		},
	)
}

func (mgr Manager) EmitContractSettlementEvent(ctx cosmos.Context, debt, valIncome cosmos.Int, contract *types.Contract) error {
	return ctx.EventManager().EmitTypedEvent(
		&types.EventSettleContract{
			Provider:   contract.Provider,
			ContractId: contract.Id,
			Service:    contract.Service.String(),
			Client:     contract.Client,
			Delegate:   contract.Delegate,
			Nonce:      contract.Nonce,
			Height:     contract.Height,
			Paid:       debt,
			Reserve:    valIncome,
		},
	)
}

func (mgr Manager) EmitValidatorPayoutEvent(ctx cosmos.Context, acc cosmos.AccAddress, rwd cosmos.Int) error {
	return ctx.EventManager().EmitTypedEvent(
		&types.ValidatorPayoutEvent{
			Validator: acc,
			Reward:    rwd,
		},
	)
}
