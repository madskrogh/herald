package e2e

import (
	"github.com/madskrogh/herald/internal/spanschema"
)

// SchemaProblems returns every way each of spans breaks the span schema in
// the spec, by span ID; a span that follows it has no entry. A span's parent
// and the spans it links to are checked by name when they are among spans.
func SchemaProblems(spans []Span) (map[string][]error, error) {
	schema, err := spanschema.Load()
	if err != nil {
		return nil, err
	}
	type spanKey struct{ trace, span string }
	names := make(map[spanKey]string, len(spans))
	for _, s := range spans {
		names[spanKey{s.TraceID, s.SpanID}] = s.Name
	}
	problems := map[string][]error{}
	for _, s := range spans {
		span := spanschema.Span{
			Name:       s.Name,
			Service:    s.ServiceName,
			Attributes: s.Attributes,
			Parent:     names[spanKey{s.TraceID, s.ParentSpanID}],
			Root:       s.ParentSpanID == "",
		}
		for _, l := range s.Links {
			if name, ok := names[spanKey{l.TraceID, l.SpanID}]; ok {
				span.Links = append(span.Links, name)
			}
		}
		if ps := schema.Check(span); len(ps) > 0 {
			problems[s.SpanID] = ps
		}
	}
	return problems, nil
}
