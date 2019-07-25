package bancorlite

import (
	"bytes"
	"github.com/coinexchain/dex/modules/bancorlite/internal/keepers"
	"github.com/coinexchain/dex/modules/bancorlite/internal/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func NewHandler(k Keeper) sdk.Handler {
	return func(ctx sdk.Context, msg sdk.Msg) sdk.Result {
		ctx.WithEventManager(sdk.NewEventManager())
		switch msg := msg.(type) {
		case types.MsgBancorTrade:
			return handleMsgBancorTrade(ctx, k, msg)
		case types.MsgBancorInit:
			return handleMsgBancorInit(ctx, k, msg)
		case types.MsgBancorCancel:
			return handleMsgBancorCancel(ctx, k, msg)
		default:
			errMsg := "Unrecognized bancorlite Msg type: %s" + msg.Type()
			return sdk.ErrUnknownRequest(errMsg).Result()
		}
	}
}

func handleMsgBancorInit(ctx sdk.Context, k Keeper, msg types.MsgBancorInit) sdk.Result {
	if bi := k.Bik.Load(ctx, msg.Stock+keepers.SymbolSeparator+msg.Money); bi != nil {
		return types.ErrBancorAlreadyExists().Result()
	}
	if !k.Axk.IsTokenExists(ctx, msg.Stock) || !k.Axk.IsTokenExists(ctx, msg.Money) {
		return types.ErrNoSuchToken().Result()
	}
	if !k.Axk.IsTokenIssuer(ctx, msg.Stock, msg.Owner) {
		return types.ErrNonOwnerIsProhibited().Result()
	}
	if msg.Money != "cet" &&
		!k.Mk.IsMarketExist(ctx, msg.Stock+keepers.SymbolSeparator+"cet") {
		return types.ErrNonMarketExist().Result()
	}
	coins := sdk.Coins{sdk.Coin{Denom: msg.Stock, Amount: msg.MaxSupply}}
	if err := k.Bxk.FreezeCoins(ctx, msg.Owner, coins); err != nil {
		return err.Result()
	}
	if err := k.Bxk.DeductFee(ctx, msg.Owner,
		sdk.NewCoins(sdk.NewCoin("cet", sdk.NewInt(k.Bik.GetParam(ctx).CreateBancorFee)))); err != nil {
		return err.Result()
	}
	bi := &keepers.BancorInfo{
		Owner:            msg.Owner,
		Stock:            msg.Stock,
		Money:            msg.Money,
		InitPrice:        msg.InitPrice,
		MaxSupply:        msg.MaxSupply,
		MaxPrice:         msg.MaxPrice,
		Price:            sdk.ZeroDec(),
		StockInPool:      msg.MaxSupply,
		MoneyInPool:      sdk.ZeroInt(),
		EnableCancelTime: msg.EnableCancelTime,
	}
	k.Bik.Save(ctx, bi)
	//ctx.EventManager().EmitEvents(sdk.Events{
	//	sdk.NewEvent(
	//		EventTypeBancorlite,
	//		sdk.NewAttribute(AttributeKeyCreateFor, bi.Token),
	//	),
	//	sdk.NewEvent(
	//		sdk.EventTypeMessage,
	//		sdk.NewAttribute(sdk.AttributeKeyModule, types.ModuleName),
	//		sdk.NewAttribute(AttributeOwner, bi.Owner.String()),
	//		sdk.NewAttribute(AttributeMaxSupply, bi.MaxSupply.String()),
	//	),
	//})
	//
	//return sdk.Result{
	//	Events: ctx.EventManager().Events(),
	//}
	return sdk.Result{}
}

func handleMsgBancorCancel(ctx sdk.Context, k Keeper, msg types.MsgBancorCancel) sdk.Result {
	bi := k.Bik.Load(ctx, msg.Stock+keepers.SymbolSeparator+msg.Money)
	if bi == nil {
		return types.ErrNoBancorExists().Result()
	}
	if !bytes.Equal(bi.Owner, msg.Owner) {
		return types.ErrNotBancorOwner().Result()
	}
	if ctx.BlockHeader().Time.Unix() < bi.EnableCancelTime {
		return types.ErrEnableCancelTimeNotArrive().Result()
	}
	if !k.Mk.IsMarketExist(ctx, msg.Stock+keepers.SymbolSeparator+"cet") {
		return types.ErrNonMarketExist().Result()
	}
	if err := k.Bxk.DeductFee(ctx, msg.Owner,
		sdk.NewCoins(sdk.NewCoin("cet", sdk.NewInt(k.Bik.GetParam(ctx).CancelBancorFee)))); err != nil {
		return err.Result()
	}
	k.Bik.Remove(ctx, bi)
	if err := k.Bxk.UnFreezeCoins(ctx, bi.Owner, sdk.NewCoins(sdk.NewCoin(bi.Stock, bi.StockInPool))); err != nil {
		return err.Result()
	}
	if err := k.Bxk.UnFreezeCoins(ctx, bi.Owner, sdk.NewCoins(sdk.NewCoin(bi.Money, bi.MoneyInPool))); err != nil {
		return err.Result()
	}
	return sdk.Result{}
}

func handleMsgBancorTrade(ctx sdk.Context, k Keeper, msg types.MsgBancorTrade) sdk.Result {
	bi := k.Bik.Load(ctx, msg.Stock+keepers.SymbolSeparator+msg.Money)
	if bi == nil {
		return types.ErrNoBancorExists().Result()
	}
	if bytes.Equal(bi.Owner, msg.Sender) {
		return types.ErrOwnerIsProhibited().Result()
	}

	stockInPool := bi.StockInPool.AddRaw(msg.Amount)
	if msg.IsBuy {
		stockInPool = bi.StockInPool.SubRaw(msg.Amount)
	}
	biNew := *bi
	if ok := biNew.UpdateStockInPool(stockInPool); !ok {
		return types.ErrStockInPoolOutofBound().Result()
	}

	diff := bi.MoneyInPool.Sub(biNew.MoneyInPool)
	coinsFromPool := sdk.Coins{sdk.Coin{Denom: msg.Money, Amount: diff}}
	coinsToPool := sdk.Coins{sdk.Coin{Denom: msg.Stock, Amount: sdk.NewInt(msg.Amount)}}
	moneyCrossLimit := msg.MoneyLimit > 0 && diff.LT(sdk.NewInt(msg.MoneyLimit))
	moneyErr := "less than"
	if msg.IsBuy {
		diff = biNew.MoneyInPool.Sub(bi.MoneyInPool)
		coinsToPool = sdk.Coins{sdk.Coin{Denom: msg.Money, Amount: diff}}
		coinsFromPool = sdk.Coins{sdk.Coin{Denom: msg.Stock, Amount: sdk.NewInt(msg.Amount)}}
		moneyCrossLimit = msg.MoneyLimit > 0 && diff.GT(sdk.NewInt(msg.MoneyLimit))
		moneyErr = "more than"
	}

	if moneyCrossLimit {
		return types.ErrMoneyCrossLimit(moneyErr).Result()
	}

	price, err := k.Mk.GetMarketLastExePrice(ctx, msg.Stock+keepers.SymbolSeparator+"cet")
	if err != nil {
		return types.ErrGetMarketPrice(err.Error()).Result()
	}
	commission := price.MulInt(sdk.NewInt(msg.Amount)).MulInt(sdk.NewInt(k.Bik.GetParam(ctx).TradeFeeRate)).QuoInt(sdk.NewInt(10000)).RoundInt()
	if err := k.Bxk.DeductFee(ctx, msg.Sender, sdk.NewCoins(sdk.NewCoin("cet", commission))); err != nil {
		return err.Result()
	}
	if err := k.Bxk.SendCoins(ctx, msg.Sender, bi.Owner, coinsToPool); err != nil {
		return err.Result()
	}
	if err := k.Bxk.FreezeCoins(ctx, bi.Owner, coinsToPool); err != nil {
		return err.Result()
	}
	if err := k.Bxk.UnFreezeCoins(ctx, bi.Owner, coinsFromPool); err != nil {
		return err.Result()
	}
	if err := k.Bxk.SendCoins(ctx, bi.Owner, msg.Sender, coinsFromPool); err != nil {
		return err.Result()
	}
	k.Bik.Save(ctx, &biNew)

	side := "Sell"
	if msg.IsBuy {
		side = "Buy"
	}
	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			EventTypeBancorlite,
			sdk.NewAttribute(AttributeKeyTradeFor, bi.Stock+keepers.SymbolSeparator+bi.Money),
		),
		sdk.NewEvent(
			sdk.EventTypeMessage,
			sdk.NewAttribute(sdk.AttributeKeyModule, types.ModuleName),
			sdk.NewAttribute(AttributeNewStockInPool, biNew.StockInPool.String()),
			sdk.NewAttribute(AttributeNewMoneyInPool, biNew.MoneyInPool.String()),
			sdk.NewAttribute(AttributeNewPrice, biNew.Price.String()),
			sdk.NewAttribute(AttributeNewPrice, biNew.Price.String()),
			sdk.NewAttribute(AttributeTradeSide, side),
			sdk.NewAttribute(AttributeCoinsFromPool, coinsFromPool.String()),
			sdk.NewAttribute(AttributeCoinsToPool, coinsToPool.String()),
		),
	})

	return sdk.Result{
		Events: ctx.EventManager().Events(),
	}
}
