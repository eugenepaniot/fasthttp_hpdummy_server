package nousage

import (
	"fasthttp_hpdummy_server/common"

	json "github.com/bytedance/sonic"
	"github.com/valyala/fasthttp"
)

// Description returns the endpoint description for startup logging
func Description() string {
	return "  - /nousage/*   -> Test endpoints for parseErrors simulation (no Usage field, wrong Content-Type, etc.)"
}

// Handler routes to appropriate no-usage test endpoints based on path suffix
// Endpoints simulate various parseErrors scenarios from tokens_usage.go:
//
//	/nousage          -> JSON without Usage field (tests "no_usage" error)
//	/nousage/wrongct  -> Wrong Content-Type header (tests "wrong_content_type" error)
//	/nousage/badjson  -> Invalid JSON body (tests "json_unmarshal_error" error)
//	/nousage/empty    -> Empty body with application/json (tests "no_usage" via EOF)
func Handler(ctx *fasthttp.RequestCtx) {
	path := common.B2s(ctx.Path())

	switch path {
	case "/nousage/wrongct":
		handleWrongContentType(ctx)
	case "/nousage/badjson":
		handleBadJSON(ctx)
	case "/nousage/empty":
		handleEmpty(ctx)
	default:
		// Default: /nousage - JSON without Usage field
		handleNoUsage(ctx)
	}
}

// handleNoUsage returns request details as JSON but WITHOUT the Usage field
// This triggers the "no_usage" parseError in tokens_usage.go
func handleNoUsage(ctx *fasthttp.RequestCtx) {
	jsonData, _ := noUsageRequestToJSON(ctx)
	common.SendJSONResponse(ctx, jsonData)
}

// handleWrongContentType returns a response with text/plain Content-Type
// This triggers the "wrong_content_type" parseError in tokens_usage.go
func handleWrongContentType(ctx *fasthttp.RequestCtx) {
	jsonData, _ := noUsageRequestToJSON(ctx)

	// Send response with wrong Content-Type (text/plain instead of application/json)
	ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
	ctx.Response.Header.SetContentLength(len(jsonData))
	common.SetConnectionHeader(ctx)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(jsonData)
}

// handleBadJSON returns invalid JSON that cannot be unmarshaled
// This triggers the "json_unmarshal_error" parseError in tokens_usage.go
func handleBadJSON(ctx *fasthttp.RequestCtx) {
	// Invalid JSON: missing closing brace, truncated string, etc.
	invalidJSON := []byte(`{"_myhostname":"test","uri":"/nousage/badjson","method":"GET","invalid json truncated`)

	ctx.Response.Header.SetContentTypeBytes(common.ContentTypeApplicationJSON)
	ctx.Response.Header.SetContentLength(len(invalidJSON))
	common.SetConnectionHeader(ctx)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(invalidJSON)
}

// handleEmpty returns an empty body with application/json Content-Type
// This triggers the "no_usage" parseError via the EOF path in tokens_usage.go
func handleEmpty(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.SetContentTypeBytes(common.ContentTypeApplicationJSON)
	ctx.Response.Header.SetContentLength(0)
	common.SetConnectionHeader(ctx)
	ctx.SetStatusCode(fasthttp.StatusOK)
	// No body set - empty response
}

// noUsageRequestToJSON converts request to JSON format with zeroed Usage field
// Reuses common.AcquireRequestJSON pool and common.PopulateRequestJSON,
// but zeros out the Usage field before marshaling to trigger "no_usage" parseError
func noUsageRequestToJSON(ctx *fasthttp.RequestCtx) ([]byte, error) {
	reqJSON := common.AcquireRequestJSON()
	defer common.ReleaseRequestJSON(reqJSON)

	// Use shared function to populate request data
	common.PopulateRequestJSON(ctx, reqJSON)

	// Zero out the Usage field to simulate "no_usage" scenario
	// All UsageStruct fields have omitempty, so zeroed values won't appear in JSON
	// tokens_usage.go checks: if usage != (usageStruct{}) - zeroed struct triggers "no_usage"
	reqJSON.Usage = common.UsageStruct{}

	return json.Marshal(reqJSON)
}
