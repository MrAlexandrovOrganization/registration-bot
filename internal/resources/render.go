package resources

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"

	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/tgfmt"
)

//go:embed texts.json
var data []byte
var texts = func() map[string]string {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		panic(err)
	}
	return m
}()

func Text(key string) string {
	if value, ok := texts[key]; ok {
		return value
	}
	return texts["command_usage"]
}

type Button struct {
	Text           string `json:"text"`
	CallbackData   string `json:"callback_data,omitempty"`
	RequestContact bool   `json:"request_contact,omitempty"`
}
type Markup struct {
	InlineKeyboard [][]Button `json:"inline_keyboard,omitempty"`
	Keyboard       [][]Button `json:"keyboard,omitempty"`
	Resize         bool       `json:"resize_keyboard,omitempty"`
	OneTime        bool       `json:"one_time_keyboard,omitempty"`
	Remove         bool       `json:"remove_keyboard,omitempty"`
}

func Render(v *pb.View) (tgfmt.HTML, Markup) {
	if v == nil {
		return tgfmt.Escape(Text("command_usage")), Markup{Remove: true}
	}
	var parts []tgfmt.HTML
	appendText := func(s string) { parts = append(parts, tgfmt.Escape(s)) }
	switch v.Kind {
	case "question":
		appendText(Text("question_" + v.Field))
	case "validation":
		appendText(Text(v.Code) + "\n" + Text("question_"+v.Field))
	case "registered", "confirm":
		parts = append(parts, tgfmt.Bold(tgfmt.Escape(Text(v.Kind+"_title"))))
		for _, f := range v.Fields {
			parts = append(parts, tgfmt.Escape("\n"+Text(f.Key)+": "), tgfmt.Code(tgfmt.Escape(f.Value)))
		}
	case "edit":
		appendText(Text("edit_title"))
	case "notice":
		appendText(Text(v.Code))
	case "help", "about", "bring", "poll":
		if v.Kind == "help" && v.Code == "help_registration" {
			appendText(Text(v.Code))
		} else {
			appendText(Text(v.Kind))
		}
	case "permissions":
		appendText(Text("permissions_title"))
		for _, f := range v.Fields {
			appendText("\n" + f.Value)
		}
	case "stats":
		appendText(Text("stats_title"))
		for _, f := range v.Fields {
			appendText("\n" + Text(f.Key) + ": " + f.Value)
		}
		for i, n := range v.Numbers {
			appendText("\n" + Text("stats_"+strconv.Itoa(i)) + ": " + strconv.FormatInt(n, 10))
		}
		appendText("\n\n" + Text("sources_hint"))
	case "sources":
		appendText(Text("sources_title"))
		if len(v.Numbers) > 0 {
			appendText(fmt.Sprintf(Text("sources_page"), v.Numbers[0]))
		}
		if len(v.Fields) == 0 {
			appendText("\n" + Text("sources_empty"))
		}
		for _, f := range v.Fields {
			label := f.Key
			switch label {
			case "":
				label = Text("sources_direct")
			case ":unknown":
				label = Text("sources_unknown")
			}
			appendText("\n" + label + ": " + f.Value)
		}
		if len(v.Numbers) > 1 {
			appendText(fmt.Sprintf(Text("sources_next"), v.Numbers[1]))
		}
	case "broadcast_preview":
		if len(v.Numbers) >= 2 {
			appendText(fmt.Sprintf(Text("preview"), v.Numbers[0], v.Numbers[1], v.Numbers[0], v.Numbers[0]))
		}
	case "broadcast_progress":
		if len(v.Numbers) > 0 {
			appendText(fmt.Sprintf(Text("progress"), v.Numbers[0], Text(v.Code)))
			for _, f := range v.Fields {
				appendText("\n" + Text(f.Key) + ": " + f.Value)
			}
		}
	case "milestone":
		if len(v.Numbers) > 0 {
			appendText(fmt.Sprintf(Text("milestone"), v.Numbers[0]))
		}
	case "sync_done":
		if len(v.Numbers) >= 2 {
			appendText(fmt.Sprintf(Text("sync_done"), v.Numbers[0], v.Numbers[1]))
		}
	default:
		appendText(Text("command_usage"))
	}
	m := Markup{Remove: true}
	if len(v.Buttons) > 0 {
		m = Markup{}
		for _, b := range v.Buttons {
			m.InlineKeyboard = append(m.InlineKeyboard, []Button{{Text: Text(b.LabelKey), CallbackData: b.Data}})
		}
	}
	if (v.Kind == "question" || v.Kind == "validation") && v.Field == "phone" && len(v.Buttons) == 0 {
		m = Markup{Keyboard: [][]Button{{{Text: Text("share_contact"), RequestContact: true}}}, Resize: true, OneTime: true}
	}
	return tgfmt.Join(parts...), m
}
