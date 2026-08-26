package platform

import "errors"

// ErrNoChange rejects an Apply whose payload equals the current version.
var ErrNoChange = errors.New("platform: no changes against current version")

// ErrInvalid wraps validation failures (callers map it to 400).
var ErrInvalid = errors.New("platform: invalid settings")

// ErrUnknownSymbol reports a symbol absent from the venue's exchangeInfo
// or not TRADING (callers map it to 400 unknown_symbol).
var ErrUnknownSymbol = errors.New("platform: unknown symbol")

// ErrNoTriangles reports a symbol/starting-asset combination that closes
// no triangle, or a starting asset absent from the selected symbols
// (callers map it to 400 no_triangles).
var ErrNoTriangles = errors.New("platform: no triangles")

// ErrForbidden wraps authorization refusals from a section Authorize
// gate (callers map it to 403).
var ErrForbidden = errors.New("platform: change not permitted for this actor")

// ErrNotFound reports an unknown version (Get/Rollback).
var ErrNotFound = errors.New("platform: version not found")

// ErrCatalogNotReady reports a Catalog that has not finished its
// bootstrap yet (empty result), distinct from "this venue genuinely has
// no markets" — the API maps it to 503, never to unknown_symbol.
var ErrCatalogNotReady = errors.New("platform: market catalog not ready yet")
