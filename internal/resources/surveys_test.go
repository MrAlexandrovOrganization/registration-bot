package resources

import (
	"html"
	"strings"
	"testing"
	"unicode/utf16"

	pb "registration.local/frontend/api"
)

func TestSurveyRenderingAndEscaping(t *testing.T) {
	v := &pb.View{Kind: "survey_broadcast", Title: "<Title &>", Text: "<script> & вопрос", Buttons: []*pb.Button{{Label: "<Да &>", Data: "qa_1_2"}}}
	text, markup := Render(v)
	if !strings.Contains(string(text), "&lt;Title &amp;&gt;") || !strings.Contains(string(text), "&lt;script&gt;") || markup.InlineKeyboard[0][0].Text != "<Да &>" || markup.InlineKeyboard[0][0].CallbackData != "qa_1_2" {
		t.Fatal(text, markup)
	}
	for _, kind := range []string{"registered", "confirm"} {
		text, _ = Render(&pb.View{Kind: kind, Fields: []*pb.Field{{Key: "name", Value: "<Name>"}, {Key: "survey_1", Value: "private answer"}}})
		if strings.Contains(string(text), "private answer") || strings.Contains(string(text), "survey_1") || !strings.Contains(string(text), "&lt;Name&gt;") {
			t.Fatal(text)
		}
	}

	text, _ = Render(&pb.View{Kind: "survey_people", Title: "<Q>", Fields: []*pb.Field{{Key: "42 · <Name>", Value: ""}}, Numbers: []int64{1, 1, 2}})
	for _, want := range []string{"&lt;Name&gt;", Text("survey_no_answer"), "/question_results 1 2"} {
		if !strings.Contains(string(text), want) {
			t.Fatal(text)
		}
	}
}

func telegramTextLength(s string) int {
	plain := strings.NewReplacer("<b>", "", "</b>", "", "<code>", "", "</code>", "").Replace(s)
	return len(utf16.Encode([]rune(html.UnescapeString(plain))))
}
func TestSurveyMessageBounds(t *testing.T) {
	// Maximum validated base form values, including two-code-unit emoji.
	base := []*pb.Field{
		{Key: "name", Value: strings.Repeat("😀", 150)},
		{Key: "birth_date", Value: "01.01.2000"},
		{Key: "group", Value: "ИУ7-41/" + strings.Repeat("1", 490)},
		{Key: "phone", Value: "79991234567"},
		{Key: "expectations", Value: strings.Repeat("😀", 500)},
		{Key: "will_drive", Value: Text("will_drive_0")},
		{Key: "trip_attendance", Value: Text("trip_attendance_0")},
	}
	people := make([]*pb.Field, 10)
	counts := make([]*pb.Field, 10)
	for i := range people {
		people[i] = &pb.Field{Key: "9223372036854775807 · " + strings.Repeat("😀", 150), Value: strings.Repeat("😀", 30)}
		counts[i] = &pb.Field{Key: strings.Repeat("😀", 30), Value: "9223372036854775807"}
	}
	for _, v := range []*pb.View{
		{Kind: "confirm", Fields: base},
		{Kind: "survey_people", Title: strings.Repeat("😀", 40), Fields: people, Numbers: []int64{1, 1, 2}},
		{Kind: "survey_results", Title: strings.Repeat("😀", 40), Fields: counts, Code: "9223372036854775807"},
		{Kind: "survey_broadcast", Title: strings.Repeat("😀", 40), Text: strings.Repeat("😀", 900)},
	} {
		text, _ := Render(v)
		if n := telegramTextLength(string(text)); n > 4096 {
			t.Fatalf("%s exceeds Telegram limit: %d", v.Kind, n)
		}
	}
}
