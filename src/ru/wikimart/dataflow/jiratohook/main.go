package main

import "encoding/json"
import "net/http"
import "log"
import "os"
import "strings"
import "bytes"
import "fmt"

type JiraHandler struct {
	DestinationHook string
	JiraBaseUrl string
}

type JiraIssueLogEntryTransition struct {
	FromStatus string `json:"from_status"`
	ToStatus string `json:"to_status"`
	Name string `json:"transitionName"`
}

type JiraIssueLogIssueFields struct {
	Summary string `json:"summary"`
	IssueLinks []JiraIssueLogIssueLink `json:"issuelinks"`
}

type JiraIssueLogIssueBase struct {
        Key string `json:"key"`
        Fields *JiraIssueLogIssueFields `json:"fields"`
}

type JiraIssueLogIssueLinkType struct {
	Name string `json:"name"`
}

type JiraIssueLogIssueLink struct {
	Type *JiraIssueLogIssueLinkType `json:"type"`
	OutwardIssue *JiraIssueLogIssueBase `json:"outwardIssue"`
	InwardIssue *JiraIssueLogIssueBase `json:"inwardIssue"`
}

type JiraIssueLogIssue struct {
	JiraIssueLogIssueBase
}

type JiraIssueLogEntry struct {
	WebhookEvent string `json:"webhookEvent"`
	Transition *JiraIssueLogEntryTransition `json:"transition"`
	Issue *JiraIssueLogIssue `json:"issue"`
}

type WebHookMessage struct {
	Text string `json:"text"`
	IconEmoji *string `json:"icon_emoji,omitempty"`
}

func (h *JiraHandler) GetScopeExceptMD(baseIssue string) string {
	return fmt.Sprintf("%s/issues/?jql=issue%%20in%%20linkedIssues(%%22%s%%22)%%20AND%%20project%%20!%%3D%%20MD", h.JiraBaseUrl, baseIssue)
}

func (h *JiraHandler) LogEvent(event *JiraIssueLogEntry) {
	log.Printf("event %s\n", event.WebhookEvent)
	if event.Issue != nil {
		log.Printf("issue %s\n", event.Issue.Key)
	}

	if event.Transition != nil {
		log.Printf("%s → %s (%s)\n", event.Transition.FromStatus, event.Transition.ToStatus, event.Transition.Name)
		if len(event.Issue.Fields.IssueLinks) > 0 {
			for _, link := range event.Issue.Fields.IssueLinks {
				if link.OutwardIssue != nil {
					log.Printf("issue link: %s (%s)\n", link.OutwardIssue.Key, link.OutwardIssue.Fields.Summary)
				}
			}
		} else { log.Printf("no issue links\n") }
	}
}

func (h *JiraHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	// decode event
	dec := json.NewDecoder(request.Body)

	var logEntry JiraIssueLogEntry
	dec.Decode(&logEntry)

	// write log entry
	h.LogEvent(&logEntry)

	// do transition processing
	if logEntry.Transition != nil {
		// process just these transitions
		isRelease := logEntry.Transition.Name == "Release"
		isDeploy := logEntry.Transition.Name == "Deploy"
		isRollback := logEntry.Transition.Name == "Rollback"

		// process just QA-issues
		if (isRelease || isDeploy || isRollback) && strings.HasPrefix(logEntry.Issue.Key, "QA-") {
			prefixText := "issue ???"
			if isRelease {
				prefixText = ":slinky: issue released"
			} else if isDeploy {
				prefixText = ":+1::skin-tone-6: issue deployed"
			} else if isRollback {
				prefixText = ":slinky2: issue rollbacked"
			}

			// base text about the root issue
			messageText := fmt.Sprintf("%s: *<%s/browse/%s|%s>* (_%s_)", prefixText, h.JiraBaseUrl, logEntry.Issue.Key, logEntry.Issue.Key, logEntry.Issue.Fields.Summary)

			// accumulated text for md and non-md entries
			// if there are MD entries, non-MD entries are skipped
			mdText := ""
			nonMdText := ""

			const MAX_NON_MD_ISSUES = 10 // it we have more non-md issues, than this const, cut the rest of them and put a short summary as the last issue
			lastNonMdIssueText := "" // if we have MAX_NON_MD_ISSUES + 1, still write the last one
			countNonMdIssues := 0

			for _, link := range logEntry.Issue.Fields.IssueLinks {
				// choose the issue, we do not care, whether is is inward or outward
				issue := link.OutwardIssue
				if issue == nil {
					issue = link.InwardIssue
				}

				if issue != nil {
					issueText := fmt.Sprintf("- *<%s/browse/%s|%s>* (_%s_)", h.JiraBaseUrl, issue.Key, issue.Key, issue.Fields.Summary)

					if strings.HasPrefix(issue.Key, "MD-") {
						mdText = mdText + "\n" + issueText
						//messageText = messageText + "\n" + issueText
					} else if link.Type != nil && link.Type.Name == "Release link" {
						countNonMdIssues++
						lastNonMdIssueText = issueText
						if countNonMdIssues < MAX_NON_MD_ISSUES {
							nonMdText = nonMdText + "\n" + issueText
						}
					}
				}
			}

			if mdText != "" {
				messageText = messageText + mdText
				if countNonMdIssues > 0 {
					messageText = messageText + "\n" + fmt.Sprintf("- ...with <%s|%d issue(s) in scope>", h.GetScopeExceptMD(logEntry.Issue.Key), countNonMdIssues)
				}
			} else if nonMdText != "" {
				messageText = messageText + nonMdText
				if countNonMdIssues > MAX_NON_MD_ISSUES {
					if MAX_NON_MD_ISSUES - countNonMdIssues == 1 { // if there's just one more issue, just print it as well
						messageText = messageText + lastNonMdIssueText
					} else {
						messageText = messageText + "\n" + fmt.Sprintf("- ...and <%s|other %d issue(s)>", h.GetScopeExceptMD(logEntry.Issue.Key), MAX_NON_MD_ISSUES - countNonMdIssues)
					}
				}
			}

			releaseEmoji := ":slinky:"
			message := WebHookMessage {
				Text: messageText,
				IconEmoji: &releaseEmoji,
			}

			postString, err := json.Marshal(message)
			
			if err != nil {
				log.Printf("error when marshalling a message: %s", err.Error())
				return
			}

			log.Printf("sending %s", postString)
			_, err = http.Post(h.DestinationHook, "application/json", bytes.NewReader(postString))
			if err != nil {
				log.Printf("error when posting to webhook: %s\n", err)
				return
			} else {
				log.Printf("post to webhook %s", postString)
			}
		}
	}

	log.Printf("\n")
}

func main() {
	args := os.Args[1:]
	if len(args) < 3 {
		log.Fatalf("not enough arguments\n./jiratohook http://jira.address localhost:8080 http://destinationwebhook")
		return
	}

	jiraBaseUrl := args[0]
	bindAddress := args[1]
	hook := args[2]

	jiraHandler := &JiraHandler {
		DestinationHook: hook,
		JiraBaseUrl: jiraBaseUrl,
	}

	srv := &http.Server {
		Addr: bindAddress,
		Handler: jiraHandler,
	}

	

	log.Fatal(srv.ListenAndServe())
}
