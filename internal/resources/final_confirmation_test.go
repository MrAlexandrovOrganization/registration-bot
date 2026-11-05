package resources

import (
	"strings"
	"testing"

	pb "registration.local/frontend/api"
)

func TestFinalConfirmationRendering(t *testing.T) {
	v := &pb.View{Kind: "notice", Code: "final_invitation", Buttons: []*pb.Button{
		{LabelKey: "final_yes", Data: "final:yes"}, {LabelKey: "final_no", Data: "final:no"},
	}}
	text, markup := Render(v)
	if !strings.Contains(string(text), Text("final_invitation")) || len(markup.InlineKeyboard) != 2 ||
		markup.InlineKeyboard[0][0].Text != "Да" || markup.InlineKeyboard[1][0].Text != "Нет" ||
		markup.InlineKeyboard[0][0].CallbackData != "final:yes" {
		t.Fatal("invalid final invitation", text, markup)
	}
	for _, kind := range []string{"final_manage", "final_status"} {
		text, _ = Render(&pb.View{Kind: kind, Code: "final_status_info", Fields: []*pb.Field{{Key: "final_source_question", Value: "<agreement &>"}}})
		if strings.Contains(string(text), "<agreement") || !strings.Contains(string(text), "&lt;agreement &amp;&gt;") {
			t.Fatal("unescaped final confirmation metadata", text)
		}
	}
}

func TestFinalDeclinePreview(t *testing.T) {
	text, markup := Render(&pb.View{Kind: "final_decline_preview", Code: "final_decline_confirm", Fields: []*pb.Field{
		{Key: "final_decline_person", Value: "<A&B>"}, {Key: "final_decline_id", Value: "42"},
	}, Buttons: []*pb.Button{
		{LabelKey: "final_decline_button", Data: "final_decline:42:yes"}, {LabelKey: "cancel", Data: "final_decline_cancel"},
	}})
	if strings.Contains(string(text), "<A&B>") || !strings.Contains(string(text), "&lt;A&amp;B&gt;") ||
		!strings.Contains(string(text), "Telegram ID: 42") || len(markup.InlineKeyboard) != 2 ||
		markup.InlineKeyboard[0][0].Text != Text("final_decline_button") || markup.InlineKeyboard[0][0].CallbackData != "final_decline:42:yes" {
		t.Fatal("unsafe or incomplete decline preview", text, markup)
	}
}

func TestFinalExtraViews(t *testing.T) {
	_, copied := Render(&pb.View{Kind: "final_invites_message_preview", Buttons: []*pb.Button{
		{Label: "Еду ✅", Data: "preview_final_0"}, {Label: "Нет 😢", Data: "preview_final_1"},
	}})
	if len(copied.InlineKeyboard) != 2 || copied.InlineKeyboard[0][0].Text != "Еду ✅" || copied.InlineKeyboard[0][0].CallbackData != "preview_final_0" {
		t.Fatal("copied preview lost the campaign buttons", copied)
	}
	text, markup := Render(&pb.View{Kind: "final_invites_preview", Code: "final_personal_preview", Fields: []*pb.Field{
		{Key: "final_decline_person", Value: "<A&B>"}, {Key: "final_decline_id", Value: "42"},
		{Key: "final_extra_count", Value: "1"}, {Key: "final_free_seats", Value: "0"},
	}, Buttons: []*pb.Button{
		{LabelKey: "final_extra_send", Data: "final_extra:11"}, {LabelKey: "cancel", Data: "final_extra_cancel:11"},
	}})
	if strings.Contains(string(text), "<A&B>") || !strings.Contains(string(text), "&lt;A&amp;B&gt;") ||
		!strings.Contains(string(text), "не бронирует место") || !strings.Contains(string(text), "Свободных мест сейчас: 0") ||
		len(markup.InlineKeyboard) != 2 || markup.InlineKeyboard[0][0].Text != Text("final_extra_send") {
		t.Fatal("unsafe or incomplete personal invitation preview", text, markup)
	}
	text, markup = Render(&pb.View{Kind: "notice", Code: "final_late_saved"})
	if !strings.Contains(string(text), "подтвердить участие не получилось") || strings.Contains(string(text), "Твоё участие подтверждено") || len(markup.InlineKeyboard) != 0 {
		t.Fatal("late answer was rendered as a successful confirmation", text, markup)
	}
}
