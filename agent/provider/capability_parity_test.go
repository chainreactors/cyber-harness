package provider

import "context"

// Compile-time capability parity guard: every optional capability the app
// asserts at runtime must be satisfied by BOTH providers, or provider=anthropic
// silently loses features (the ListModels bug). If either line stops compiling,
// a capability gap was reintroduced.
var (
	_ interface {
		ListModels(context.Context) ([]string, error)
	} = (*OpenAIProvider)(nil)
	_ interface {
		ListModels(context.Context) ([]string, error)
	} = (*AnthropicProvider)(nil)
	_ StreamingProvider            = (*OpenAIProvider)(nil)
	_ StreamingProvider            = (*AnthropicProvider)(nil)
	_ WebSearchProvider            = (*OpenAIProvider)(nil)
	_ WebSearchProvider            = (*AnthropicProvider)(nil)
	_ interface{ DisableImages() } = (*OpenAIProvider)(nil)
	_ interface{ DisableImages() } = (*AnthropicProvider)(nil)
)
