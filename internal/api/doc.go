// Handlers are thin controllers only — each holds the service object(s) it
// needs (constructor-injected in server.go's wiring, rule 1), decodes/validates
// the request, calls ONE service method, maps the result to JSON. No business
// logic lives here.
package api
