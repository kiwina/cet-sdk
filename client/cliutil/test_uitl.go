package cliutil

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
)

func SetViperWithArgs(args []string) {
	viper.Reset()
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		idx := strings.Index(arg, "=")
		if idx < 0 {
			continue
		}
		viper.Set(arg[2:idx], arg[idx+1:])
	}
}

func TestQueryCmd(t *testing.T, cmdFactory func() *cobra.Command,
	args string, expectedPath string, expectedParam interface{}) {

	oldCliQuery := CliQuery
	defer func() {
		CliQuery = oldCliQuery
	}()

	executed := false
	CliQuery = func(cdc *codec.Codec, path string, param interface{}) error {
		executed = true
		require.Equal(t, path, expectedPath)
		require.Equal(t, param, expectedParam)
		return nil
	}

	cmd := cmdFactory()
	cmd.SetArgs(strings.Split(args, " "))
	err := cmd.Execute()
	require.NoError(t, err)
	require.True(t, executed)
}
