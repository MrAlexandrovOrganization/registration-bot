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
