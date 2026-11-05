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
