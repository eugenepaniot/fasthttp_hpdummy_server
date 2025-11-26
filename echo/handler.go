package echo

import (
	"fasthttp_hpdummy_server/common"

	json "github.com/bytedance/sonic"

	"github.com/valyala/fasthttp"
)

// Description returns the endpoint description for startup logging
func Description() string {
	return "  - /           -> Echo server (returns request details as JSON)"
}

// Handler handles echo requests - returns request details as JSON
// Optimized for high performance with minimal allocations
func Handler(ctx *fasthttp.RequestCtx) {
	jsonData, _ := requestToJSON(ctx)

	common.SendJSONResponse(ctx, jsonData)
}

// requestToJSON converts request to JSON format
// Optimized to minimize allocations by using B2s for zero-copy conversions
func requestToJSON(ctx *fasthttp.RequestCtx) ([]byte, error) {
	reqJSON := common.AcquireRequestJSON()
	defer common.ReleaseRequestJSON(reqJSON)

	// Use shared function to populate request data
	common.PopulateRequestJSON(ctx, reqJSON)

	// Marshal to JSON and return
	// Note: The marshaled data is a copy, so it's safe to release reqJSON after this
	return json.Marshal(reqJSON)
}
