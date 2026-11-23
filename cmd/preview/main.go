// Command preview renders an embedded fixture pack and serves the static
// HTML on localhost - no outbound network call.
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/rudra184/baitbet/internal/content"
	"github.com/rudra184/baitbet/internal/render"
)

func fixturePack() content.Pack {
	return content.Pack{
		Version:  "2026-11-21.1",
		Scenario: "network_reauth",
		Email: content.EmailContent{
			FromName:    "IT Helpdesk",
			Subject:     "Action needed: VPN session expired",
			BodyHTML:    "Your VPN session expired. Re-authenticate to keep working.",
			ButtonLabel: "Re-authenticate",
		},
		Debrief: content.DebriefContent{
			Title:        "Verify the request",
			Intro:        "This was a security awareness exercise.",
			NextSteps:    []string{"Check unexpected requests through the portal or contact you already use.", "Report suspicious messages using your usual Outlook reporting process."},
			ClosingNote:  "Training preview. You can close this page.",
			LogoAssetKey: "mark",
		},
	}
}

func main() {
	pack := fixturePack()

	mux := http.NewServeMux()
	mux.HandleFunc("/email", renderHandler(func() (string, error) { return render.Email(pack) }))
	mux.HandleFunc("/debrief", renderHandler(func() (string, error) { return render.Debrief(pack) }))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Local preview, no network access required.")
		fmt.Fprintln(w, "  GET /email    - the recipient email body")
		fmt.Fprintln(w, "  GET /debrief  - the form-free debrief landing page")
	})

	const addr = "127.0.0.1:8765"
	log.Printf("baitbet preview listening on http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func renderHandler(fn func() (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		html, err := fn()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, html)
	}
}
