package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/rushikeshg25/tick"
)

// runVerify is the only component that can observe a duplicate. Each
// generator sees only its own output, so uniqueness across the cluster has to
// be checked somewhere that sees all of it.
func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	targets := fs.String("targets", "", "comma-separated generator base urls")
	duration := fs.Duration("duration", 3*time.Minute, "how long to run")
	every := fs.Duration("every", time.Second, "drain interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targets == "" {
		return fmt.Errorf("verify: -targets is required")
	}
	urls := strings.Split(*targets, ",")

	client := &http.Client{Timeout: 5 * time.Second}
	seen := make(map[tick.ID]string)
	duplicates := 0
	collected := 0
	unreachable := map[string]int{}

	deadline := time.Now().Add(*duration)
	ticker := time.NewTicker(*every)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		<-ticker.C

		for _, u := range urls {
			ids, err := drain(client, u)
			if err != nil {
				// Being unable to reach a generator is expected: the scenario
				// disconnects them on purpose. It is not a uniqueness failure.
				unreachable[u]++
				continue
			}
			for _, id := range ids {
				collected++
				if prev, ok := seen[id]; ok {
					duplicates++
					log.Printf("DUPLICATE %d: first from %s, again from %s (node %d, seq %d, at %s)",
						id, prev, u, id.Node(), id.Seq(), id.Time().Format(time.RFC3339Nano))
					continue
				}
				seen[id] = u
			}
		}
	}

	fmt.Printf("collected %d IDs from %d generators\n", collected, len(urls))
	fmt.Printf("unreachable polls: %v\n", unreachable)
	fmt.Printf("duplicates: %d\n", duplicates)

	if duplicates > 0 {
		return fmt.Errorf("verify: %d duplicate IDs; invariant I1 violated", duplicates)
	}
	if collected == 0 {
		return fmt.Errorf("verify: collected nothing; the cluster never generated")
	}
	fmt.Println("OK: every ID was unique")
	return nil
}

func drain(client *http.Client, base string) ([]tick.ID, error) {
	resp, err := client.Get(strings.TrimRight(base, "/") + "/drain")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var ids []tick.ID
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, err
	}
	return ids, nil
}
