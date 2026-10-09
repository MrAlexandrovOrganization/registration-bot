package resources

import (
	"strings"
	"testing"

	pb "registration.local/frontend/api"
)

func TestEscapingAndButtons(t *testing.T) {
	text, m := Render(&pb.View{Kind: "confirm", Fields: []*pb.Field{{Key: "name", Value: "<b>A&B</b>"}}, Buttons: []*pb.Button{{LabelKey: "confirm", Data: "c:1:2:confirm"}}})
	if strings.Contains(string(text), "<b>A&B</b>") || !strings.Contains(string(text), "&lt;b&gt;A&amp;B&lt;/b&gt;") {
		t.Fatal(text)
	}
	if m.InlineKeyboard[0][0].CallbackData != "c:1:2:confirm" {
		t.Fatal(m)
	}
}
func TestAllSurveyResourcesExist(t *testing.T) {
	for _, field := range []string{"name", "birth_date", "group", "phone", "expectations", "will_drive", "trip_attendance"} {
		if texts[field] == "" || texts["question_"+field] == "" {
			t.Fatal(field)
		}
	}
}

func TestRegistrationCompletedView(t *testing.T) {
	text, markup := Render(&pb.View{Kind: "notice", Code: "registration_completed"})
	if !strings.Contains(string(text), "Спасибо за регистрацию") || len(markup.InlineKeyboard) != 0 {
		t.Fatal("permanent reminder must have no navigation buttons")
	}
	text, markup = Render(&pb.View{Kind: "registered", Fields: []*pb.Field{{Key: "name", Value: "<A&B>"}}, Buttons: []*pb.Button{{LabelKey: "edit", Data: "c:3:edit"}}})
	if strings.Contains(string(text), "Спасибо за регистрацию") || !strings.Contains(string(text), "&lt;A&amp;B&gt;") || !strings.Contains(string(text), Text("registration_reminder")) || len(markup.InlineKeyboard) != 1 {
		t.Fatal("questionnaire must show escaped data, practical reminder and editing")
	}
	if strings.Contains(string(text), "18:30") || strings.Contains(string(text), "05.11") {
		t.Fatal("questionnaire must not duplicate trip logistics")
	}
	text, _ = Render(&pb.View{Kind: "about"})
	if !strings.Contains(string(text), "06.11 в 18:30") || !strings.Contains(string(text), "у Ноги") || !strings.Contains(string(text), "05.11") {
		t.Fatal("about must contain the meeting and participation reminder")
	}
}

func TestLegacyRegistrationCompletedView(t *testing.T) {
	text, markup := Render(&pb.View{Kind: "registered", Code: "registration_completed", Buttons: []*pb.Button{
		{LabelKey: "edit", Data: "c:3:edit"},
		{LabelKey: "about_button", Data: "info:about"},
		{LabelKey: "bring_button", Data: "info:bring"},
	}})
	if !strings.Contains(string(text), "Спасибо за регистрацию") || strings.Contains(string(text), Text("registered_title")) {
		t.Fatal("expected completion text instead of questionnaire")
	}
	if len(markup.InlineKeyboard) != 3 || markup.InlineKeyboard[0][0].CallbackData != "c:3:edit" || len(markup.Keyboard) != 0 || markup.Remove {
		t.Fatal("completion must retain registered-user inline buttons")
	}
}

func TestPhoneEditKeyboards(t *testing.T) {
	for _, kind := range []string{"question", "validation"} {
		_, markup := Render(&pb.View{Kind: kind, Field: "phone", Code: "invalid_phone", Buttons: []*pb.Button{{LabelKey: "cancel", Data: "c:2:cancel"}}})
		if len(markup.InlineKeyboard) != 0 || len(markup.Keyboard) != 2 || !markup.Keyboard[0][0].RequestContact || markup.Keyboard[1][0].Text != Text("cancel") || markup.Keyboard[1][0].CallbackData != "" || markup.Keyboard[1][0].RequestContact {
			t.Fatal("phone edit must combine contact sharing and cancellation in one reply keyboard")
		}
	}
	for _, view := range []*pb.View{{Kind: "contact_keyboard"}, {Kind: "question", Field: "phone"}, {Kind: "validation", Field: "phone", Code: "invalid_phone"}} {
		text, markup := Render(view)
		if len(markup.InlineKeyboard) != 0 || len(markup.Keyboard) != 1 || !markup.Keyboard[0][0].RequestContact || !markup.Resize || !markup.OneTime {
			t.Fatal("missing contact-sharing reply keyboard")
		}
		if strings.Contains(string(text), "/cancel") {
			t.Fatal("contact prompt must not require a command")
		}
		if view.Kind == "contact_keyboard" && (string(text) != Text("share_contact") || strings.Contains(string(text), Text("question_phone"))) {
			t.Fatal("keyboard caption must not repeat the phone question")
		}
	}
	text, markup := Render(&pb.View{Kind: "notice", Code: "edit_cancelled"})
	if string(text) != Text("edit_cancelled") || !markup.Remove || len(markup.InlineKeyboard) != 0 || len(markup.Keyboard) != 0 {
		t.Fatal("cancellation must remove the contact keyboard")
	}
}

func TestRegistrationOnlyViews(t *testing.T) {
	text, _ := Render(&pb.View{Kind: "stats", Fields: []*pb.Field{{Key: "stats_0", Value: "4"}, {Key: "stats_7", Value: "1"}}})
	if !strings.Contains(string(text), "Всего пользователей: 4") || strings.Contains(string(text), "Подтвердили участие") {
		t.Fatal("invalid registration statistics")
	}
	text, _ = Render(&pb.View{Kind: "help", Code: "help_registration"})
	if strings.Contains(string(text), "/poll") || strings.Contains(string(text), "yes, maybe") {
		t.Fatal("disabled participation advertised")
	}
}

func TestPublicHelpAndContentErrorsDoNotExposeOperatorCommands(t *testing.T) {
	for _, view := range []*pb.View{{Kind: "help_public"}, {Kind: "notice", Code: "invalid_content"}} {
		text, _ := Render(view)
		for _, forbidden := range []string{"права", "право", "рол", "admin", "staff", "counselor", "аудитори", "/my_permissions", "/stats", "/sources", "/export", "/broadcast", "/grant_permission"} {
			if strings.Contains(strings.ToLower(string(text)), forbidden) {
				t.Fatalf("public view exposes %q", forbidden)
			}
		}
		if !strings.Contains(string(text), "/help") || !strings.Contains(string(text), "/start") {
			t.Fatal("missing public navigation")
		}
	}
	text, _ := Render(&pb.View{Kind: "help_public"})
	for _, command := range []string{"/start", "/cancel", "/about", "/bring", "/help"} {
		if !strings.Contains(string(text), command) {
			t.Fatal(command)
		}
	}
}

func TestSourceStats(t *testing.T) {
	text, _ := Render(&pb.View{Kind: "sources", Numbers: []int64{1, 2}, Fields: []*pb.Field{
		{Key: "", Value: "3"}, {Key: ":unknown", Value: "4"}, {Key: "<b>A&B</b>", Value: "5"},
	}})
	for _, want := range []string{"Без метки: 3", "Неизвестен до начала учёта: 4", "&lt;b&gt;A&amp;B&lt;/b&gt;: 5", "/sources 2"} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	fields := make([]*pb.Field, 20)
	for i := range fields {
		fields[i] = &pb.Field{Key: strings.Repeat("a", 64), Value: "9223372036854775807"}
	}
	text, _ = Render(&pb.View{Kind: "sources", Fields: fields, Numbers: []int64{1, 2}})
	if len([]rune(text)) > 4096 {
		t.Fatal("source page exceeds Telegram message limit")
	}
}
