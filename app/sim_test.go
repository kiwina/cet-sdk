package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	dbm "github.com/tendermint/tm-db"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/simapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authsim "github.com/cosmos/cosmos-sdk/x/auth/simulation"
	"github.com/cosmos/cosmos-sdk/x/bank"
	distrsim "github.com/cosmos/cosmos-sdk/x/distribution/simulation"
	"github.com/cosmos/cosmos-sdk/x/genaccounts"
	govsim "github.com/cosmos/cosmos-sdk/x/gov/simulation"
	paramsim "github.com/cosmos/cosmos-sdk/x/params/simulation"
	"github.com/cosmos/cosmos-sdk/x/simulation"
	slashingsim "github.com/cosmos/cosmos-sdk/x/slashing/simulation"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingsim "github.com/cosmos/cosmos-sdk/x/staking/simulation"
	"github.com/cosmos/cosmos-sdk/x/supply"

	aliassim "github.com/coinexchain/dex/modules/alias/simulation"
	"github.com/coinexchain/dex/modules/asset"
	assetsim "github.com/coinexchain/dex/modules/asset/simulation"
	bancorsim "github.com/coinexchain/dex/modules/bancorlite/simulation"
	commentsim "github.com/coinexchain/dex/modules/comment/simulation"
	distrxim "github.com/coinexchain/dex/modules/distributionx/simulation"
	marketsim "github.com/coinexchain/dex/modules/market/simulation"
	"github.com/coinexchain/dex/types"
)

func init() {
	flag.StringVar(&genesisFile, "Genesis", "", "custom simulation genesis file; cannot be used with params file")
	flag.StringVar(&paramsFile, "Params", "", "custom simulation params file which overrides any random params; cannot be used with genesis")
	flag.StringVar(&exportParamsPath, "ExportParamsPath", "", "custom file path to save the exported params JSON")
	flag.IntVar(&exportParamsHeight, "ExportParamsHeight", 0, "height to which export the randomly generated params")
	flag.StringVar(&exportStatePath, "ExportStatePath", "", "custom file path to save the exported app state JSON")
	flag.StringVar(&exportStatsPath, "ExportStatsPath", "", "custom file path to save the exported simulation statistics JSON")
	flag.Int64Var(&seed, "Seed", 42, "simulation random seed")
	flag.IntVar(&initialBlockHeight, "InitialBlockHeight", 1, "initial block to start the simulation")
	flag.IntVar(&numBlocks, "NumBlocks", 500, "number of blocks")
	flag.IntVar(&blockSize, "BlockSize", 200, "operations per block")
	flag.BoolVar(&enabled, "Enabled", false, "enable the simulation")
	flag.BoolVar(&verbose, "Verbose", false, "verbose log output")
	flag.BoolVar(&lean, "Lean", false, "lean simulation log output")
	flag.BoolVar(&commit, "Commit", false, "have the simulation commit")
	flag.IntVar(&period, "Period", 1, "run slow invariants only once every period assertions")
	flag.BoolVar(&onOperation, "SimulateEveryOperation", false, "run slow invariants every operation")
	flag.BoolVar(&allInvariants, "PrintAllInvariants", false, "print all invariants if a broken invariant is found")
	flag.Int64Var(&genesisTime, "GenesisTime", 0, "override genesis UNIX time instead of using a random UNIX time")
}

// helper function for populating input for SimulateFromSeed
// TODO: clean up this function along with the simulation refactor
func getSimulateFromSeedInput(tb testing.TB, w io.Writer, app *CetChainApp) (
	testing.TB, io.Writer, *baseapp.BaseApp, simulation.AppStateFn, int64,
	simulation.WeightedOperations, sdk.Invariants, int, int, int, int, string,
	bool, bool, bool, bool, bool, map[string]bool) {

	exportParams := exportParamsPath != ""

	return tb, w, app.BaseApp, appStateFn, seed,
		testAndRunTxs(app), invariants(app),
		initialBlockHeight, numBlocks, exportParamsHeight, blockSize,
		exportStatsPath, exportParams, commit, lean, onOperation, allInvariants, app.ModuleAccountAddrs()
}

func appStateFn(
	r *rand.Rand, accs []simulation.Account,
) (appState json.RawMessage, simAccs []simulation.Account, chainID string, genesisTimestamp time.Time) {

	cdc := MakeCodec()

	if genesisTime == 0 {
		genesisTimestamp = simulation.RandTimestamp(r)
	} else {
		genesisTimestamp = time.Unix(genesisTime, 0)
	}

	switch {
	case paramsFile != "" && genesisFile != "":
		panic("cannot provide both a genesis file and a params file")

	case genesisFile != "":
		appState, simAccs, chainID = AppStateFromGenesisFileFn(r, accs, genesisTimestamp)

	case paramsFile != "":
		appParams := make(simulation.AppParams)
		bz, err := ioutil.ReadFile(paramsFile)
		if err != nil {
			panic(err)
		}

		cdc.MustUnmarshalJSON(bz, &appParams)
		appState, simAccs, chainID = appStateRandomizedFn(r, accs, genesisTimestamp, appParams)

	default:
		appParams := make(simulation.AppParams)
		appState, simAccs, chainID = appStateRandomizedFn(r, accs, genesisTimestamp, appParams)
	}

	return appState, simAccs, chainID, genesisTimestamp
}

// TODO refactor out random initialization code to the modules
func appStateRandomizedFn(
	r *rand.Rand, accs []simulation.Account, genesisTimestamp time.Time, appParams simulation.AppParams,
) (json.RawMessage, []simulation.Account, string) {

	cdc := MakeCodec()
	genesisState := ModuleBasics.DefaultGenesis()

	var (
		amount             int64
		numInitiallyBonded int64
	)

	appParams.GetOrGenerate(cdc, simapp.StakePerAccount, &amount, r,
		func(r *rand.Rand) { amount = int64(r.Intn(1e12)) + 1e13 })
	appParams.GetOrGenerate(cdc, simapp.InitiallyBondedValidators, &amount, r,
		func(r *rand.Rand) { numInitiallyBonded = int64(r.Intn(250)) })

	numAccs := int64(len(accs))
	if numInitiallyBonded > numAccs {
		numInitiallyBonded = numAccs
	}

	fmt.Printf(
		`Selected randomly generated parameters for simulated genesis:
{
  stake_per_account: "%v",
  initially_bonded_validators: "%v"
}
`, amount, numInitiallyBonded,
	)

	GenGenesisAccounts(cdc, r, accs, genesisTimestamp, amount, numInitiallyBonded, genesisState)
	simapp.GenAuthGenesisState(cdc, r, appParams, genesisState)
	simapp.GenBankGenesisState(cdc, r, appParams, genesisState)
	GenSupplyGenesisState(cdc, amount, numInitiallyBonded, int64(len(accs)), genesisState)
	simapp.GenGovGenesisState(cdc, r, appParams, genesisState)
	//simapp.GenMintGenesisState(cdc, r, appParams, genesisState)
	simapp.GenDistrGenesisState(cdc, r, appParams, genesisState)
	stakingGen := GenStakingGenesisState(cdc, r, accs, amount, numAccs, numInitiallyBonded, appParams, genesisState)
	simapp.GenSlashingGenesisState(cdc, r, stakingGen, appParams, genesisState)
	GenAssetGenesisState(cdc, accs, amount, numInitiallyBonded, genesisState)

	appState, err := MakeCodec().MarshalJSON(genesisState)
	if err != nil {
		panic(err)
	}

	return appState, accs, "simulation"
}

// GenStakingGenesisState generates a random GenesisState for staking
func GenStakingGenesisState(
	cdc *codec.Codec, r *rand.Rand, accs []simulation.Account, amount, numAccs, numInitiallyBonded int64,
	ap simulation.AppParams, genesisState map[string]json.RawMessage,
) staking.GenesisState {

	stakingGenesis := staking.NewGenesisState(
		staking.NewParams(
			func(r *rand.Rand) time.Duration {
				var v time.Duration
				ap.GetOrGenerate(cdc, simulation.UnbondingTime, &v, r,
					func(r *rand.Rand) {
						v = simulation.ModuleParamSimulator[simulation.UnbondingTime](r).(time.Duration)
					})
				return v
			}(r),
			func(r *rand.Rand) uint16 {
				var v uint16
				ap.GetOrGenerate(cdc, simulation.MaxValidators, &v, r,
					func(r *rand.Rand) {
						v = simulation.ModuleParamSimulator[simulation.MaxValidators](r).(uint16)
					})
				return v
			}(r),
			7,
			types.CET,
		),
		nil,
		nil,
	)

	var (
		validators  []staking.Validator
		delegations []staking.Delegation
	)

	valAddrs := make([]sdk.ValAddress, numInitiallyBonded)
	for i := 0; i < int(numInitiallyBonded); i++ {
		valAddr := sdk.ValAddress(accs[i].Address)
		valAddrs[i] = valAddr

		validator := staking.NewValidator(valAddr, accs[i].PubKey, staking.Description{})
		validator.Tokens = sdk.NewInt(amount)
		validator.DelegatorShares = sdk.NewDec(amount)
		delegation := staking.NewDelegation(accs[i].Address, valAddr, sdk.NewDec(amount))
		validators = append(validators, validator)
		delegations = append(delegations, delegation)
	}

	stakingGenesis.Validators = validators
	stakingGenesis.Delegations = delegations

	fmt.Printf("Selected randomly generated staking parameters:\n%s\n", codec.MustMarshalJSONIndent(cdc, stakingGenesis.Params))
	genesisState[staking.ModuleName] = cdc.MustMarshalJSON(stakingGenesis)

	return stakingGenesis
}

// GenSupplyGenesisState generates a random GenesisState for supply
func GenSupplyGenesisState(cdc *codec.Codec, amount, numInitiallyBonded, numAccs int64, genesisState map[string]json.RawMessage) {
	totalSupply := sdk.NewInt(amount * (numAccs + numInitiallyBonded))
	supplyGenesis := supply.NewGenesisState(
		sdk.NewCoins(sdk.NewCoin(types.CET, totalSupply)),
	)

	fmt.Printf("Generated supply parameters:\n%s\n", codec.MustMarshalJSONIndent(cdc, supplyGenesis))
	genesisState[supply.ModuleName] = cdc.MustMarshalJSON(supplyGenesis)
}

func GenGenesisAccounts(
	cdc *codec.Codec, r *rand.Rand, accs []simulation.Account,
	genesisTimestamp time.Time, amount, numInitiallyBonded int64,
	genesisState map[string]json.RawMessage,
) {

	var genesisAccounts []genaccounts.GenesisAccount

	fmt.Println("total amount : ", amount*int64(len(accs)), "; accounts : ", len(accs))
	// randomly generate some genesis accounts
	for i, acc := range accs {
		coins := sdk.Coins{sdk.NewCoin(types.CET, sdk.NewInt(amount))}
		bacc := auth.NewBaseAccountWithAddress(acc.Address)
		bacc.SetCoins(coins)

		var gacc genaccounts.GenesisAccount

		// Only consider making a vesting account once the initial bonded validator
		// set is exhausted due to needing to track DelegatedVesting.
		if int64(i) > numInitiallyBonded && r.Intn(100) < 50 {
			var (
				vacc    auth.VestingAccount
				endTime int64
			)

			startTime := genesisTimestamp.Unix()

			// Allow for some vesting accounts to vest very quickly while others very slowly.
			if r.Intn(100) < 50 {
				endTime = int64(simulation.RandIntBetween(r, int(startTime), int(startTime+(60*60*24*30))))
			} else {
				endTime = int64(simulation.RandIntBetween(r, int(startTime), int(startTime+(60*60*12))))
			}

			if startTime == endTime {
				endTime++
			}

			if r.Intn(100) < 50 {
				vacc = auth.NewContinuousVestingAccount(&bacc, startTime, endTime)
			} else {
				vacc = auth.NewDelayedVestingAccount(&bacc, endTime)
			}

			var err error
			gacc, err = genaccounts.NewGenesisAccountI(vacc)
			if err != nil {
				panic(err)
			}
		} else {
			gacc = genaccounts.NewGenesisAccount(&bacc)
		}

		genesisAccounts = append(genesisAccounts, gacc)
	}

	genesisState[genaccounts.ModuleName] = cdc.MustMarshalJSON(genesisAccounts)
}

func GenAssetGenesisState(cdc *codec.Codec, accs []simulation.Account, amount, numInitiallyBonded int64,
	genesisState map[string]json.RawMessage) {

	tokenTotalSupply := sdk.NewInt(amount * (int64(len(accs)) + numInitiallyBonded))
	assetGenesis := asset.DefaultGenesisState()
	baseToken, _ := asset.NewToken("CoinEx Chain Native Token",
		"cet",
		tokenTotalSupply,
		accs[0].Address,
		false,
		true,
		false,
		false,
		"www.coinex.org",
		"A public chain built for the decentralized exchange",
		"",
	)
	assetGenesis.Tokens = append(assetGenesis.Tokens, baseToken)

	genesisState[asset.ModuleName] = cdc.MustMarshalJSON(assetGenesis)
}

// TODO: add description
func testAndRunTxs(app *CetChainApp) []simulation.WeightedOperation {
	cdc := MakeCodec()
	ap := make(simulation.AppParams)

	if paramsFile != "" {
		bz, err := ioutil.ReadFile(paramsFile)
		if err != nil {
			panic(err)
		}

		cdc.MustUnmarshalJSON(bz, &ap)
	}

	getWeightOrDefault := getIntOrDefaultFn(ap, cdc)
	return []simulation.WeightedOperation{
		{
			Weight: getWeightOrDefault(simapp.OpWeightDeductFee, 5),
			Op:     authsim.SimulateDeductFee(app.accountKeeper, app.supplyKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgSend, 100),
			Op:     bank.SimulateMsgSend(app.accountKeeper, app.bankKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightSingleInputMsgMultiSend, 10),
			Op:     bank.SimulateSingleInputMsgMultiSend(app.accountKeeper, app.bankKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgSetWithdrawAddress, 50),
			Op:     distrsim.SimulateMsgSetWithdrawAddress(app.accountKeeper, app.distrKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgWithdrawDelegationReward, 50),
			Op:     distrsim.SimulateMsgWithdrawDelegatorReward(app.accountKeeper, app.distrKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgWithdrawValidatorCommission, 50),
			Op:     distrsim.SimulateMsgWithdrawValidatorCommission(app.accountKeeper, app.distrKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightSubmitVotingSlashingTextProposal, 5),
			Op:     govsim.SimulateSubmittingVotingAndSlashingForProposal(app.govKeeper, govsim.SimulateTextProposalContent),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightSubmitVotingSlashingCommunitySpendProposal, 5),
			Op:     govsim.SimulateSubmittingVotingAndSlashingForProposal(app.govKeeper, distrsim.SimulateCommunityPoolSpendProposalContent(app.distrKeeper)),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightSubmitVotingSlashingParamChangeProposal, 5),
			Op:     govsim.SimulateSubmittingVotingAndSlashingForProposal(app.govKeeper, paramsim.SimulateParamChangeProposalContent),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgDeposit, 100),
			Op:     govsim.SimulateMsgDeposit(app.govKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgCreateValidator, 100),
			Op:     stakingsim.SimulateMsgCreateValidator(app.accountKeeper, app.stakingKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgEditValidator, 5),
			Op:     stakingsim.SimulateMsgEditValidator(app.stakingKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgDelegate, 100),
			Op:     stakingsim.SimulateMsgDelegate(app.accountKeeper, app.stakingKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgUndelegate, 100),
			Op:     stakingsim.SimulateMsgUndelegate(app.accountKeeper, app.stakingKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgBeginRedelegate, 100),
			Op:     stakingsim.SimulateMsgBeginRedelegate(app.accountKeeper, app.stakingKeeper),
		},
		{
			Weight: getWeightOrDefault(simapp.OpWeightMsgUnjail, 100),
			Op:     slashingsim.SimulateMsgUnjail(app.slashingKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgAliasUpdate, 100),
			Op:     aliassim.SimulateMsgAliasUpdate(app.aliasKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgIssueToken, 150),
			Op:     assetsim.SimulateMsgIssueToken(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgBurnToken, 50),
			Op:     assetsim.SimulateMsgBurnToken(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgTransferOwnership, 60),
			Op:     assetsim.SimulateMsgTransferOwnership(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgMintToken, 50),
			Op:     assetsim.SimulateMsgMintToken(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgForbidToken, 50),
			Op:     assetsim.SimulateMsgForbidToken(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgUnForbidToken, 50),
			Op:     assetsim.SimulateMsgUnForbidToken(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgAddTokenWhitelist, 70),
			Op:     assetsim.SimulateMsgAddTokenWhitelist(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgRemoveTokenWhitelist, 60),
			Op:     assetsim.SimulateMsgRemoveTokenWhitelist(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgForbidAddr, 30),
			Op:     assetsim.SimulateMsgForbidAddr(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgUnForbidAddr, 25),
			Op:     assetsim.SimulateMsgUnForbidAddr(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgModifyTokenInfo, 40),
			Op:     assetsim.SimulateMsgModifyTokenInfo(app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgBancorInit, 100),
			Op:     bancorsim.SimulateMsgBancorInit(app.assetKeeper, app.bancorKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgBancorTrade, 100),
			Op:     bancorsim.SimulateMsgBancorTrade(app.accountKeeper, app.bancorKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgBancorCancel, 100),
			Op:     bancorsim.SimulateMsgBancorCancel(app.bancorKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightCreateNewThread, 100),
			Op:     commentsim.SimulateCreateNewThread(app.commentKeeper, app.assetKeeper, app.accountKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightCreateCommentRefs, 100),
			Op:     commentsim.SimulateCreateCommentRefs(app.commentKeeper, app.assetKeeper, app.accountKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgDonateToCommunityPool, 100),
			Op:     distrxim.SimulateMsgDonateToCommunityPool(app.accountKeeper, app.distrxKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgCreateTradingPair, 100),
			Op:     marketsim.SimulateMsgCreateTradingPair(app.marketKeeper, app.assetKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgCancelTradingPair, 100),
			Op:     marketsim.SimulateMsgCancelTradingPair(app.marketKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgModifyPricePrecision, 100),
			Op:     marketsim.SimulateMsgModifyPricePrecision(app.marketKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgCreateOrder, 100),
			Op:     marketsim.SimulateMsgCreateOrder(app.marketKeeper, app.accountKeeper),
		},
		{
			Weight: getWeightOrDefault(OpWeightMsgCancelOrder, 100),
			Op:     marketsim.SimulateMsgCancelOrder(app.marketKeeper),
		},
	}
}

func getIntOrDefaultFn(ap simulation.AppParams, cdc *codec.Codec) func(string, int) int {
	return func(key string, defaultValue int) int {
		var v int
		ap.GetOrGenerate(cdc, key, &v, nil,
			func(_ *rand.Rand) {
				v = defaultValue
			})
		return v
	}
}

func invariants(app *CetChainApp) []sdk.Invariant {
	// TODO: fix PeriodicInvariants, it doesn't seem to call individual invariants for a period of 1
	// Ref: https://github.com/cosmos/cosmos-sdk/issues/4631
	if period == 1 {
		return app.crisisKeeper.Invariants()
	}
	return simulation.PeriodicInvariants(app.crisisKeeper.Invariants(), period, 0)
}

// Pass this in as an option to use a dbStoreAdapter instead of an IAVLStore for simulation speed.
func fauxMerkleModeOpt(bapp *baseapp.BaseApp) {
	bapp.SetFauxMerkleMode()
}

// Profile with:
// /usr/local/go/bin/go test -benchmem -run=^$ github.com/cosmos/cosmos-sdk/simapp -bench ^BenchmarkFullAppSimulation$ -Commit=true -cpuprofile cpu.out
func BenchmarkFullAppSimulation(b *testing.B) {
	logger := log.NewNopLogger()

	var db dbm.DB
	dir, _ := ioutil.TempDir("", "goleveldb-app-sim")
	db, _ = sdk.NewLevelDB("Simulation", dir)
	defer func() {
		db.Close()
		_ = os.RemoveAll(dir)
	}()
	app := NewCetChainApp(logger, db, nil, true, 0)

	// Run randomized simulation
	// TODO: parameterize numbers, save for a later PR
	_, params, simErr := simulation.SimulateFromSeed(getSimulateFromSeedInput(b, os.Stdout, app))

	// export state and params before the simulation error is checked
	if exportStatePath != "" {
		fmt.Println("Exporting app state...")
		appState, _, err := app.ExportAppStateAndValidators(false, nil)
		if err != nil {
			fmt.Println(err)
			b.Fail()
		}
		err = ioutil.WriteFile(exportStatePath, []byte(appState), 0644)
		if err != nil {
			fmt.Println(err)
			b.Fail()
		}
	}

	if exportParamsPath != "" {
		fmt.Println("Exporting simulation params...")
		paramsBz, err := json.MarshalIndent(params, "", " ")
		if err != nil {
			fmt.Println(err)
			b.Fail()
		}

		err = ioutil.WriteFile(exportParamsPath, paramsBz, 0644)
		if err != nil {
			fmt.Println(err)
			b.Fail()
		}
	}

	if simErr != nil {
		fmt.Println(simErr)
		b.FailNow()
	}

	if commit {
		fmt.Println("\nGoLevelDB Stats")
		fmt.Println(db.Stats()["leveldb.stats"])
		fmt.Println("GoLevelDB cached block size", db.Stats()["leveldb.cachedblock"])
	}
}

func TestFullAppSimulation(t *testing.T) {
	if !enabled {
		t.Skip("Skipping application simulation")
	}

	var logger log.Logger

	if verbose {
		logger = log.TestingLogger()
	} else {
		logger = log.NewNopLogger()
	}

	var db dbm.DB
	dir, _ := ioutil.TempDir("", "goleveldb-app-sim")
	db, _ = sdk.NewLevelDB("Simulation", dir)

	defer func() {
		db.Close()
		os.RemoveAll(dir)
	}()

	app := NewCetChainApp(logger, db, nil, true, 0, fauxMerkleModeOpt)
	require.Equal(t, "CoinExChainApp", app.Name())

	// Run randomized simulation
	_, params, simErr := simulation.SimulateFromSeed(getSimulateFromSeedInput(t, os.Stdout, app))

	// export state and params before the simulation error is checked
	if exportStatePath != "" {
		fmt.Println("Exporting app state...")
		appState, _, err := app.ExportAppStateAndValidators(false, nil)
		require.NoError(t, err)

		err = ioutil.WriteFile(exportStatePath, []byte(appState), 0644)
		require.NoError(t, err)
	}

	if exportParamsPath != "" {
		fmt.Println("Exporting simulation params...")
		fmt.Println(params)
		paramsBz, err := json.MarshalIndent(params, "", " ")
		require.NoError(t, err)

		err = ioutil.WriteFile(exportParamsPath, paramsBz, 0644)
		require.NoError(t, err)
	}

	require.NoError(t, simErr)

	if commit {
		// for memdb:
		// fmt.Println("Database Size", db.Stats()["database.size"])
		fmt.Println("\nGoLevelDB Stats")
		fmt.Println(db.Stats()["leveldb.stats"])
		fmt.Println("GoLevelDB cached block size", db.Stats()["leveldb.cachedblock"])
	}
}

func TestAppImportExport(t *testing.T) {
	if !enabled {
		t.Skip("Skipping application import/export simulation")
	}

	var logger log.Logger
	if verbose {
		logger = log.TestingLogger()
	} else {
		logger = log.NewNopLogger()
	}

	var db dbm.DB
	dir, _ := ioutil.TempDir("", "goleveldb-app-sim")
	db, _ = sdk.NewLevelDB("Simulation", dir)

	defer func() {
		db.Close()
		os.RemoveAll(dir)
	}()

	app := NewCetChainApp(logger, db, nil, true, 0, fauxMerkleModeOpt)
	require.Equal(t, "CoinExChainApp", app.Name())

	// Run randomized simulation
	_, simParams, simErr := simulation.SimulateFromSeed(getSimulateFromSeedInput(t, os.Stdout, app))

	// export state and simParams before the simulation error is checked
	if exportStatePath != "" {
		fmt.Println("Exporting app state...")
		appState, _, err := app.ExportAppStateAndValidators(false, nil)
		require.NoError(t, err)

		err = ioutil.WriteFile(exportStatePath, []byte(appState), 0644)
		require.NoError(t, err)
	}

	if exportParamsPath != "" {
		fmt.Println("Exporting simulation params...")
		simParamsBz, err := json.MarshalIndent(simParams, "", " ")
		require.NoError(t, err)

		err = ioutil.WriteFile(exportParamsPath, simParamsBz, 0644)
		require.NoError(t, err)
	}

	require.NoError(t, simErr)

	if commit {
		// for memdb:
		// fmt.Println("Database Size", db.Stats()["database.size"])
		fmt.Println("\nGoLevelDB Stats")
		fmt.Println(db.Stats()["leveldb.stats"])
		fmt.Println("GoLevelDB cached block size", db.Stats()["leveldb.cachedblock"])
	}

	fmt.Printf("Exporting genesis...\n")

	appState, _, err := app.ExportAppStateAndValidators(false, []string{})
	require.NoError(t, err)
	fmt.Printf("Importing genesis...\n")

	newDir, _ := ioutil.TempDir("", "goleveldb-app-sim-2")
	newDB, _ := sdk.NewLevelDB("Simulation-2", dir)

	defer func() {
		newDB.Close()
		_ = os.RemoveAll(newDir)
	}()

	newApp := NewCetChainApp(log.NewNopLogger(), newDB, nil, true, 0, fauxMerkleModeOpt)
	require.Equal(t, "CoinExChainApp", newApp.Name())

	var genesisState simapp.GenesisState
	err = app.cdc.UnmarshalJSON(appState, &genesisState)
	if err != nil {
		panic(err)
	}

	ctxB := newApp.NewContext(true, abci.Header{Height: app.LastBlockHeight()})
	newApp.mm.InitGenesis(ctxB, genesisState)

	fmt.Printf("Comparing stores...\n")
	ctxA := app.NewContext(true, abci.Header{Height: app.LastBlockHeight()})

	type StoreKeysPrefixes struct {
		A        sdk.StoreKey
		B        sdk.StoreKey
		Prefixes [][]byte
	}

	storeKeysPrefixes := []StoreKeysPrefixes{
		{app.keyMain, newApp.keyMain, [][]byte{}},
		{app.keyAccount, newApp.keyAccount, [][]byte{}},
		{app.keyStaking, newApp.keyStaking,
			[][]byte{
				staking.UnbondingQueueKey, staking.RedelegationQueueKey, staking.ValidatorQueueKey,
			}}, // ordering may change but it doesn't matter
		{app.keySlashing, newApp.keySlashing, [][]byte{}},
		//{app.keyMint, newApp.keyMint, [][]byte{}},
		{app.keyDistr, newApp.keyDistr, [][]byte{}},
		{app.keySupply, newApp.keySupply, [][]byte{}},
		{app.keyParams, newApp.keyParams, [][]byte{}},
		{app.keyGov, newApp.keyGov, [][]byte{}},
	}

	for _, storeKeysPrefix := range storeKeysPrefixes {
		storeKeyA := storeKeysPrefix.A
		storeKeyB := storeKeysPrefix.B
		prefixes := storeKeysPrefix.Prefixes
		storeA := ctxA.KVStore(storeKeyA)
		storeB := ctxB.KVStore(storeKeyB)
		kvA, kvB, count, equal := sdk.DiffKVStores(storeA, storeB, prefixes)
		fmt.Printf("Compared %d key/value pairs between %s and %s\n", count, storeKeyA, storeKeyB)
		require.True(t, equal, simapp.GetSimulationLog(storeKeyA.Name(), app.cdc, newApp.cdc, kvA, kvB))
	}

}

func TestAppSimulationAfterImport(t *testing.T) {
	if !enabled {
		t.Skip("Skipping application simulation after import")
	}

	var logger log.Logger
	if verbose {
		logger = log.TestingLogger()
	} else {
		logger = log.NewNopLogger()
	}

	dir, _ := ioutil.TempDir("", "goleveldb-app-sim")
	db, _ := sdk.NewLevelDB("Simulation", dir)

	defer func() {
		db.Close()
		os.RemoveAll(dir)
	}()

	app := NewCetChainApp(logger, db, nil, true, 0, fauxMerkleModeOpt)
	require.Equal(t, "CoinExChainApp", app.Name())

	// Run randomized simulation
	stopEarly, params, simErr := simulation.SimulateFromSeed(getSimulateFromSeedInput(t, os.Stdout, app))

	// export state and params before the simulation error is checked
	if exportStatePath != "" {
		fmt.Println("Exporting app state...")
		appState, _, err := app.ExportAppStateAndValidators(false, nil)
		require.NoError(t, err)

		err = ioutil.WriteFile(exportStatePath, []byte(appState), 0644)
		require.NoError(t, err)
	}

	if exportParamsPath != "" {
		fmt.Println("Exporting simulation params...")
		paramsBz, err := json.MarshalIndent(params, "", " ")
		require.NoError(t, err)

		err = ioutil.WriteFile(exportParamsPath, paramsBz, 0644)
		require.NoError(t, err)
	}

	require.NoError(t, simErr)

	if commit {
		// for memdb:
		// fmt.Println("Database Size", db.Stats()["database.size"])
		fmt.Println("GoLevelDB Stats")
		fmt.Println(db.Stats()["leveldb.stats"])
		fmt.Println("GoLevelDB cached block size", db.Stats()["leveldb.cachedblock"])
	}

	if stopEarly {
		// we can't export or import a zero-validator genesis
		fmt.Printf("We can't export or import a zero-validator genesis, exiting test...\n")
		return
	}

	fmt.Printf("Exporting genesis...\n")

	appState, _, err := app.ExportAppStateAndValidators(true, []string{})
	if err != nil {
		panic(err)
	}

	fmt.Printf("Importing genesis...\n")

	newDir, _ := ioutil.TempDir("", "goleveldb-app-sim-2")
	newDB, _ := sdk.NewLevelDB("Simulation-2", dir)

	defer func() {
		newDB.Close()
		os.RemoveAll(newDir)
	}()

	newApp := NewCetChainApp(log.NewNopLogger(), newDB, nil, true, 0, fauxMerkleModeOpt)
	require.Equal(t, "CoinExChainApp", newApp.Name())
	newApp.InitChain(abci.RequestInitChain{
		AppStateBytes: appState,
	})

	// Run randomized simulation on imported app
	_, _, err = simulation.SimulateFromSeed(getSimulateFromSeedInput(t, os.Stdout, newApp))
	require.Nil(t, err)
}

// TODO: Make another test for the fuzzer itself, which just has noOp txs
// and doesn't depend on the application.
func TestAppStateDeterminism(t *testing.T) {
	if !enabled {
		t.Skip("Skipping application simulation")
	}

	numSeeds := 3
	numTimesToRunPerSeed := 5
	appHashList := make([]json.RawMessage, numTimesToRunPerSeed)

	for i := 0; i < numSeeds; i++ {
		seed := rand.Int63()
		for j := 0; j < numTimesToRunPerSeed; j++ {
			logger := log.NewNopLogger()
			db := dbm.NewMemDB()
			app := NewCetChainApp(logger, db, nil, true, 0)

			fmt.Printf(
				"Running non-determinism simulation; seed: %d/%d (%d), attempt: %d/%d\n",
				i+1, numSeeds, seed, j+1, numTimesToRunPerSeed,
			)

			_, _, err := simulation.SimulateFromSeed(
				t, os.Stdout, app.BaseApp, appStateFn, seed, testAndRunTxs(app),
				[]sdk.Invariant{}, 1, numBlocks, exportParamsHeight,
				blockSize, "", false, commit, lean,
				false, false, app.ModuleAccountAddrs(),
			)
			require.NoError(t, err)

			appHash := app.LastCommitID().Hash
			appHashList[j] = appHash
		}
		for k := 1; k < numTimesToRunPerSeed; k++ {
			require.Equal(t, appHashList[0], appHashList[k], "appHash list: %v", appHashList)
		}
	}
}

func BenchmarkInvariants(b *testing.B) {
	logger := log.NewNopLogger()
	dir, _ := ioutil.TempDir("", "goleveldb-app-invariant-bench")
	db, _ := sdk.NewLevelDB("simulation", dir)

	defer func() {
		db.Close()
		_ = os.RemoveAll(dir)
	}()

	app := NewCetChainApp(logger, db, nil, true, 0)
	exportParams := exportParamsPath != ""

	// 2. Run parameterized simulation (w/o invariants)
	_, params, simErr := simulation.SimulateFromSeed(
		b, ioutil.Discard, app.BaseApp, appStateFn, seed, testAndRunTxs(app),
		[]sdk.Invariant{}, initialBlockHeight, numBlocks, exportParamsHeight, blockSize,
		exportStatsPath, exportParams, commit, lean, onOperation, false, app.ModuleAccountAddrs(),
	)

	// export state and params before the simulation error is checked
	if exportStatePath != "" {
		fmt.Println("Exporting app state...")
		appState, _, err := app.ExportAppStateAndValidators(false, nil)
		if err != nil {
			fmt.Println(err)
			b.Fail()
		}
		err = ioutil.WriteFile(exportStatePath, []byte(appState), 0644)
		if err != nil {
			fmt.Println(err)
			b.Fail()
		}
	}

	if exportParamsPath != "" {
		fmt.Println("Exporting simulation params...")
		paramsBz, err := json.MarshalIndent(params, "", " ")
		if err != nil {
			fmt.Println(err)
			b.Fail()
		}

		err = ioutil.WriteFile(exportParamsPath, paramsBz, 0644)
		if err != nil {
			fmt.Println(err)
			b.Fail()
		}
	}

	if simErr != nil {
		fmt.Println(simErr)
		b.FailNow()
	}

	ctx := app.NewContext(true, abci.Header{Height: app.LastBlockHeight() + 1})

	// 3. Benchmark each invariant separately
	//
	// NOTE: We use the crisis keeper as it has all the invariants registered with
	// their respective metadata which makes it useful for testing/benchmarking.
	for _, cr := range app.crisisKeeper.Routes() {
		b.Run(fmt.Sprintf("%s/%s", cr.ModuleName, cr.Route), func(b *testing.B) {
			if res, stop := cr.Invar(ctx); stop {
				fmt.Printf("broken invariant at block %d of %d\n%s", ctx.BlockHeight()-1, numBlocks, res)
				b.FailNow()
			}
		})
	}
}
