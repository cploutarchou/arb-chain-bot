package screener

import (
	"errors"

	"github.com/shopspring/decimal"
)

// ErrNoQuote reports that the calculator (or another read) has no
// current quote for the requested venue/pair — never guessed or
// defaulted to zero.
var ErrNoQuote = errors.New("screener: no quote for requested venue/pair")

// CalculatorRequest is the decoded body of POST /screener/calculator
// (design §7).
type CalculatorRequest struct {
	Base, Quote         string
	BuyVenue, SellVenue Venue
	SizeQuote           decimal.Decimal
	TransferFeeQuote    *decimal.Decimal
	OverrideBuyFeeBps   *decimal.Decimal
	OverrideSellFeeBps  *decimal.Decimal
}

// CalculatorResult is the wire response of POST /screener/calculator.
type CalculatorResult struct {
	BuyAsk      decimal.Decimal `json:"buy_ask"`
	SellBid     decimal.Decimal `json:"sell_bid"`
	SizeBase    decimal.Decimal `json:"size_base"`
	Gross       decimal.Decimal `json:"gross"`
	FeesBuy     decimal.Decimal `json:"fees_buy"`
	FeesSell    decimal.Decimal `json:"fees_sell"`
	TransferFee decimal.Decimal `json:"transfer_fee"`
	Net         decimal.Decimal `json:"net"`
	NetBps      decimal.Decimal `json:"net_bps"`
	LiquidityOK bool            `json:"liquidity_ok"`
}

// Calculate is the golden-tested core of POST /screener/calculator: the
// same net-of-fees model spreads.go uses (design §3 "no-transfer model"
// extended with an OPTIONAL transfer fee, since the calculator is the
// one place an operator may supply one), applied to one caller-chosen
// size instead of the venue's full top-of-book.
func Calculate(book *Book, fees VenueFeeLookup, req CalculatorRequest) (CalculatorResult, error) {
	if !req.SizeQuote.IsPositive() {
		return CalculatorResult{}, ErrInvalid
	}
	byVenue := book.QuotesFor(req.Base, req.Quote)
	buyQ, ok := byVenue[req.BuyVenue]
	if !ok || !buyQ.Ask.IsPositive() {
		return CalculatorResult{}, ErrNoQuote
	}
	sellQ, ok := byVenue[req.SellVenue]
	if !ok || !sellQ.Bid.IsPositive() {
		return CalculatorResult{}, ErrNoQuote
	}

	buyFeeBps, err := resolveFee(req.OverrideBuyFeeBps, fees, req.BuyVenue)
	if err != nil {
		return CalculatorResult{}, err
	}
	sellFeeBps, err := resolveFee(req.OverrideSellFeeBps, fees, req.SellVenue)
	if err != nil {
		return CalculatorResult{}, err
	}

	sizeBase := req.SizeQuote.Div(buyQ.Ask)
	proceeds := sizeBase.Mul(sellQ.Bid)
	gross := proceeds.Sub(req.SizeQuote)
	feesBuy := req.SizeQuote.Mul(buyFeeBps.Div(decTenK))
	feesSell := proceeds.Mul(sellFeeBps.Div(decTenK))
	transferFee := decimal.Zero
	if req.TransferFeeQuote != nil {
		transferFee = *req.TransferFeeQuote
	}
	net := gross.Sub(feesBuy).Sub(feesSell).Sub(transferFee)
	netBps := net.Div(req.SizeQuote).Mul(decTenK)

	buyNotional := buyQ.Ask.Mul(buyQ.AskQty)
	sellNotional := sellQ.Bid.Mul(sellQ.BidQty)
	liquidityQuote := buyNotional
	if sellNotional.LessThan(liquidityQuote) {
		liquidityQuote = sellNotional
	}

	return CalculatorResult{
		BuyAsk: buyQ.Ask, SellBid: sellQ.Bid, SizeBase: sizeBase,
		Gross: gross, FeesBuy: feesBuy, FeesSell: feesSell, TransferFee: transferFee,
		Net: net, NetBps: netBps,
		LiquidityOK: req.SizeQuote.LessThanOrEqual(liquidityQuote),
	}, nil
}

func resolveFee(override *decimal.Decimal, fees VenueFeeLookup, v Venue) (decimal.Decimal, error) {
	if override != nil {
		return *override, nil
	}
	bps, ok := fees(v)
	if !ok {
		return decimal.Decimal{}, ErrNoQuote
	}
	return bps, nil
}
