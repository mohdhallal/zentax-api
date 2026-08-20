// This is just a dummy example need to be adjusted on prod
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/platform/api_clients"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: credentials <internal|external> [nexus_account_id]")
		os.Exit(1)
	}

	mode := os.Args[1]

	key := uuid.New()

	switch mode {
	case "internal":
		secret := make([]byte, 64) //nolint:makezero // rand.Read requires pre-allocated buffer
		if _, err := rand.Read(secret); err != nil {
			fmt.Fprintf(os.Stderr, "failed to generate secret: %v\n", err)
			os.Exit(1)
		}
		secretB64 := base64.RawURLEncoding.EncodeToString(secret)
		hash := sha512.Sum512(secret)
		hashB64 := base64.RawURLEncoding.EncodeToString(hash[:])
		fmt.Printf("Key:         %s\n", key)
		fmt.Printf("Secret:      %s\n", secretB64)
		fmt.Printf("Secret Hash: %s\n", hashB64)
		fmt.Println()
		fmt.Printf(
			"INSERT INTO internal_api_keys (app_name, key, secret_hash) VALUES ('<APP_NAME>', '%s', '%s');\n",
			key, hashB64,
		)

	case "external":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: credentials external <nexus_account_id>")
			os.Exit(1)
		}
		nexusAccountID, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid nexus_account_id: %v\n", err)
			os.Exit(1)
		}

		// External API key provisioning flow:
		//
		// This cmd is a one-off bootstrapping tool. The production flow should be triggered
		// automatically on nexus account creation via a dedicated endpoint, wired as follows:
		//
		//   Handler (modules/auth/handlers/) receives a POST /auth/nexus-api-keys request.
		//
		//   Use case (modules/auth/usecases/external_auth.go — ExternalAuth.StoreExternalAPIKey)
		//   calls platform/api_clients.NexusInternalAPI to provision a key via the nexus internal
		//   service, then persists the returned key/secret via NexusAccountAPIKeyRepo into the
		//   nexus_accounts_api_keys table.
		//
		//   Auth is already handled: RequireExternalAuth middleware (external server) reads
		//   X-Account-Id and sibling headers injected by the upstream gateway and populates
		//   app.Requester in context — handlers access the caller identity via
		//   app.GetRequester(r.Context()). No credential lookup is needed in the handler or
		//   use case layer.
		//
		//   Container wiring: bootstrap/container.go — NexusAccountAPIKeyUseCases is already
		//   wired as authusecases.NewExternalAuth(authpg.NewNexusAccountAPIKeyRepo(db)).
		//
		// TODO: replace NewMockNexusInternalAPIClient with NewNexusInternalAPIClient once
		// the nexus internal API is reachable, and load cfg from config.Load().NexusInternalAPI.
		cfg := &config.NexusInternalAPIConfig{
			BaseURL:        "http://localhost:8080",
			TimeoutMs:      5000,
			DefaultRPS:     1,
			DefaultKeyType: "DVS",
		}
		client := api_clients.NewMockNexusInternalAPIClient(cfg)
		result, err := client.CreateAPIKey(context.Background(), nexusAccountID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create nexus API key: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Nexus Account ID: %d\n", nexusAccountID)
		fmt.Printf("API Key:          %s\n", result.Key)
		fmt.Printf("API Secret:       %s\n", result.Secret)
		fmt.Println()
		fmt.Printf(
			"INSERT INTO nexus_accounts_api_keys (nexus_account_id, api_key, api_secret) VALUES (%d, '%s', '%s');\n",
			nexusAccountID, result.Key, result.Secret,
		)

	default:
		fmt.Println("Usage: credentials <internal|external> [nexus_account_id]")
		os.Exit(1)
	}
}
