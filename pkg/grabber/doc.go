// Package grabber contains the parsing rules used while importing external
// pages.
//
// Keep framework and browser quirks here. The main application should provide
// callbacks for effects such as downloading resources, deciding whether to
// persist a resource, and mapping imported documents to local paths. This keeps
// new parsing rules testable without a database, HTTP server, or admin UI.
package grabber
