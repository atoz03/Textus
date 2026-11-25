package backend

import (
	"textus/backend/ai/web"
)

// Textus AI's web search, as the app runs it: the web package reaching the
// web the app's way (its proxy, its name), the search service Settings
// chose, and what the composer's Web search button can do.

// searchService is the search service Textus's own web_search uses: the
// one choice names, "auto" or none the first whose key is saved; nil
// without one.
func (a *App) searchService(choice, searxngURL string) *web.Service {
	service, _ := web.ResolveService(choice, searxngURL, func(id string) string {
		key, _ := a.providerKey(id)
		return key
	})
	return service
}

// webSearchStatus is what the composer's Web search button can do with the
// provider and model in use (src/platform/bindings/ai.ts WebSearchStatus).
type webSearchStatus struct {
	// Native: the provider searches by itself.
	Native bool `json:"native"`
	// Service is Textus's own search service in use otherwise.
	Service *web.Backend `json:"service"`
}

func init() {
	web.Net.Transport = agentTransport{appTransport}
	web.Net.Proxy = proxyURL
	web.Net.UserAgent = userAgent
	command("web_search_status", func(c *Call, a struct {
		Provider      string  `json:"provider"`
		Model         *string `json:"model"`
		SearchService *string `json:"searchService"`
		SearxngURL    *string `json:"searxngUrl"`
	}) (webSearchStatus, error) {
		if web.NativeSearchFor(a.Provider, deref(a.Model)) != nil {
			return webSearchStatus{Native: true}, nil
		}
		var status webSearchStatus
		if s := c.app.searchService(deref(a.SearchService), deref(a.SearxngURL)); s != nil {
			status.Service = &s.Backend
		}
		return status, nil
	})
}
