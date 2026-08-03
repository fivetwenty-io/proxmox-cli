package cli

import "strings"

// emptyDataMarker is the text the generated API bindings use when a request
// succeeded but carried no payload.
const emptyDataMarker = "empty data in response"

// IsEmptyDataResponse reports whether err is the generated binding's complaint
// that the server answered 2xx with no payload.
//
// Several Proxmox endpoints answer a successful write with `{"data": null}` —
// updating a built-in realm, or an API token that was not regenerated, are the
// common cases. The bindings are generated from a schema that declares a return
// value for those endpoints, so they surface the empty body as an error even
// though nothing went wrong, and the command would report a failed write that
// in fact took effect.
//
// Only call this where an empty response is the documented success shape. It
// matches on the binding's message rather than a sentinel because the bindings
// build the error with fmt.Errorf and export nothing to compare against.
func IsEmptyDataResponse(err error) bool {
	return err != nil && strings.Contains(err.Error(), emptyDataMarker)
}
