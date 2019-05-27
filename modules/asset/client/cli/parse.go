package cli

import (
	"fmt"
	"github.com/coinexchain/dex/modules/asset"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/viper"
)

func parseIssueFlags(owner sdk.AccAddress) (*asset.MsgIssueToken, error) {
	for _, flag := range issueTokenFlags {
		if viper.GetString(flag) == "" {
			return nil, fmt.Errorf("--%s flag is a noop, pls see help : "+
				"$ cetcli tx asset issue-token -h", flag)
		}
	}

	msg := asset.NewMsgIssueToken(
		viper.GetString(FlagName),
		viper.GetString(FlagSymbol),
		viper.GetInt64(FlagTotalSupply),
		owner,
		viper.GetBool(FlagMintable),
		viper.GetBool(FlagBurnable),
		viper.GetBool(FlagAddrFreezable),
		viper.GetBool(FlagTokenFreezable))

	return &msg, nil
}

func parseTransferOwnershipFlags(orginalOwner sdk.AccAddress) (*asset.MsgTransferOwnership, error) {
	for _, flag := range transferOwnershipFlags {
		if viper.GetString(flag) == "" {
			return nil, fmt.Errorf("--%s flag is a noop, pls see help : "+
				"$ cetcli tx asset transfer-ownership -h", flag)
		}
	}

	newOwner, _ := sdk.AccAddressFromBech32(viper.GetString(FlagNewOwner))
	msg := asset.NewMsgTransferOwnership(
		viper.GetString(FlagSymbol),
		orginalOwner,
		newOwner,
	)

	return &msg, nil
}
