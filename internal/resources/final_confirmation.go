package resources

import (
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/tgfmt"
)

func renderFinalConfirmation(v *pb.View) tgfmt.HTML {
	parts := []tgfmt.HTML{tgfmt.Escape(Text(v.Code))}
	for _, f := range v.Fields {
		parts = append(parts, tgfmt.Escape("\n"+Text(f.Key)+": "+f.Value))
	}
	return tgfmt.Join(parts...)
}
