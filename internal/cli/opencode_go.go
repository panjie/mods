package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/panjie/mods/internal/providerinfo"
	"github.com/panjie/mods/internal/session"
)

func discoverOpenCodeGoModelsContext(ctx context.Context, protocol, baseURL, key, sessionID string) ([]string, map[string]string, []string, error) {
	return discoverOpenCodeGoModelsWithClientContext(ctx, &http.Client{Timeout: 15 * time.Second}, protocol, baseURL, key, sessionID)
}

func discoverOpenCodeGoModelsWithClientContext(ctx context.Context, client *http.Client, protocol, baseURL, key, sessionID string) ([]string, map[string]string, []string, error) {
	if !providerinfo.IsOpenCodeGoURL(baseURL) {
		return nil, nil, nil, fmt.Errorf("not an official OpenCode Go endpoint")
	}
	if sessionID == "" {
		sessionID = session.NewID()
	}
	ids, err := fetchModelIDsWithClientContext(ctx, client, providerinfo.OpenCodeGoModelsURL, "Authorization", "Bearer "+key,
		providerinfo.OpenCodeHeaders(baseURL, sessionID))
	if err != nil {
		return nil, nil, nil, err
	}
	models, endpoints, unknown := filterOpenCodeGoModels(protocol, baseURL, ids)
	return models, endpoints, unknown, nil
}

func filterOpenCodeGoModels(protocol, baseURL string, ids []string) ([]string, map[string]string, []string) {
	models := []string{}
	endpoints := map[string]string{}
	unknown := []string{}
	for _, id := range ids {
		route, ok := providerinfo.OpenCodeGoRoute(baseURL, id)
		if !ok {
			unknown = append(unknown, id)
			continue
		}
		if route.Protocol == protocol {
			models = append(models, id)
			if protocol == "openai" {
				endpoints[id] = route.Endpoint
			}
		}
	}
	return models, endpoints, unknown
}
