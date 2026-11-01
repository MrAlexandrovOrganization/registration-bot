package resources

import (
	"fmt"

	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/tgfmt"
)

func renderSurvey(v *pb.View) tgfmt.HTML {
	var parts []tgfmt.HTML
	plain := func(s string) { parts = append(parts, tgfmt.Escape(s)) }
	title := func(s string) { parts = append(parts, tgfmt.Bold(tgfmt.Escape(s))) }
	switch v.Kind {
	case "survey_question", "survey_broadcast", "survey_preview":
		if v.PhotoFileId != "" {
			plain(v.Text)
		} else {
			title(v.Title)
			plain("\n\n" + v.Text)
		}
	case "survey_manage", "survey_notice":
		switch v.Code {
		case "survey_preview_info", "survey_preview_registration":
			if len(v.Numbers) == 2 {
				plain(fmt.Sprintf(Text(v.Code), v.Numbers[0], v.Numbers[1], v.Numbers[0]))
				if v.Text != "" {
					plain(Text("survey_audience_label") + Text("audience_"+v.Text))
				}
			}
		case "survey_existing_open", "survey_existing_closed":
			if len(v.Numbers) == 2 {
				plain(fmt.Sprintf(Text(v.Code), v.Numbers[0], v.Numbers[1], v.Numbers[1]))
			}
		case "survey_published":
			if len(v.Numbers) == 2 {
				plain(fmt.Sprintf(Text(v.Code), v.Numbers[0], v.Numbers[1], v.Numbers[1], v.Numbers[0]))
			}
		default:
			plain(Text(v.Code))
		}
	case "survey_list":
		title(Text("survey_list_title"))
		for _, f := range v.Fields {
			plain("\n" + f.Key + ": " + Text("survey_status_"+f.Value))
		}
		if len(v.Fields) == 0 {
			plain("\n" + Text("survey_empty"))
		}
		if len(v.Numbers) > 1 {
			plain(fmt.Sprintf(Text("survey_list_next"), v.Numbers[1]))
		}
	case "survey_results":
		title(v.Title)
		plain("\n" + Text("survey_results_title"))
		for _, f := range v.Fields {
			plain("\n" + f.Key + ": " + f.Value)
		}
		plain(Text("survey_unanswered") + v.Code)
	case "survey_people":
		title(v.Title)
		plain("\n" + Text("survey_people_title"))
		for _, f := range v.Fields {
			answer := f.Value
			if answer == "" {
				answer = Text("survey_no_answer")
			}
			plain("\n" + f.Key + ": " + answer)
		}
		if len(v.Fields) == 0 {
			plain("\n" + Text("survey_empty"))
		}
		if len(v.Numbers) > 2 {
			plain(fmt.Sprintf(Text("survey_people_next"), v.Numbers[0], v.Numbers[2]))
		}
	}
	return tgfmt.Join(parts...)
}
