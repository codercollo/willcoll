package notify

import (
	"errors"
	"fmt"
	"text/template/parse"

	txttemplate "text/template"
)

// Actor identifies which real recipient a template renders for — Tenant or
// Landlord (spec Phase 8.2). This is display grouping only; it never changes
// how a template is rendered or sent.
type Actor string

const (
	ActorTenant   Actor = "tenant"
	ActorLandlord Actor = "landlord"
)

// TemplateDef is one of the SIX SMS templates this system actually renders
// (Phase 8.2) — Vars lists every placeholder a caller in this codebase
// actually populates for that template (grep the internal/*.SendTemplate
// call sites before adding one; never a placeholder the backend won't fill).
// BrandName is deliberately excluded from Vars — SendTemplate injects it
// itself on every template, never from user input.
type TemplateDef struct {
	Key   string
	Actor Actor
	Vars  []string
}

// Templates is the closed set (spec Phase 8.2) — no "add new template"
// capability exists because nothing else in internal/notify/templates/ is
// ever loaded by SendTemplate outside of this list (ledger_drift_alert.tmpl
// is an internal ops alert, not Tenant/Landlord facing, and stays out of
// this UI-editable set).
var Templates = []TemplateDef{
	{Key: "tenant_rent_due.tmpl", Actor: ActorTenant, Vars: []string{"Amount", "Period"}},
	{Key: "tenant_rent_received.tmpl", Actor: ActorTenant, Vars: []string{"Amount", "Period", "Breakdown", "Reference"}},
	{Key: "tenant_arrears_notice.tmpl", Actor: ActorTenant, Vars: []string{"Amount"}},
	{Key: "tenant_welcome.tmpl", Actor: ActorTenant, Vars: []string{"RentDueDay"}},
	{Key: "landlord_digest.tmpl", Actor: ActorLandlord, Vars: []string{"Amount", "Properties"}},
	{Key: "landlord_remittance_confirmed.tmpl", Actor: ActorLandlord, Vars: []string{"Amount", "Period"}},
}

var ErrUnknownTemplate = errors.New("unknown template")

func lookupTemplate(key string) (TemplateDef, bool) {
	for _, t := range Templates {
		if t.Key == key {
			return t, true
		}
	}
	return TemplateDef{}, false
}

// DefaultBody returns the template's shipped-with-the-binary source text —
// what SendTemplate falls back to when an Organization hasn't saved its own
// override.
func DefaultBody(key string) (string, error) {
	if _, ok := lookupTemplate(key); !ok {
		return "", ErrUnknownTemplate
	}
	raw, err := templatesFS.ReadFile("templates/" + key)
	if err != nil {
		return "", fmt.Errorf("read default template %s: %w", key, err)
	}
	return string(raw), nil
}

// ValidateTemplateBody parses body as a Go text/template and rejects any
// field reference outside {{.BrandName}} plus that template's own Vars — a
// referenced variable the backend will never populate is a save-time error
// here, never a silently-blank SMS later (spec Phase 8.2).
func ValidateTemplateBody(key, body string) error {
	def, ok := lookupTemplate(key)
	if !ok {
		return ErrUnknownTemplate
	}

	allowed := make(map[string]bool, len(def.Vars)+1)
	allowed["BrandName"] = true
	for _, v := range def.Vars {
		allowed[v] = true
	}

	tmpl, err := txttemplate.New(key).Parse(body)
	if err != nil {
		return fmt.Errorf("invalid template syntax: %w", err)
	}

	for _, field := range collectFields(tmpl.Tree.Root) {
		if !allowed[field] {
			return fmt.Errorf("template references unknown variable %q", field)
		}
	}
	return nil
}

// collectFields walks a template's parse tree and returns every top-level
// field name referenced (the "X" in {{.X}}, {{if .X}}, {{if .Y}}...{{end}}).
func collectFields(node parse.Node) []string {
	var out []string
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		switch v := n.(type) {
		case *parse.ListNode:
			if v == nil {
				return
			}
			for _, child := range v.Nodes {
				walk(child)
			}
		case *parse.ActionNode:
			walk(v.Pipe)
		case *parse.IfNode:
			walk(v.Pipe)
			walk(v.List)
			walk(v.ElseList)
		case *parse.WithNode:
			walk(v.Pipe)
			walk(v.List)
			walk(v.ElseList)
		case *parse.RangeNode:
			walk(v.Pipe)
			walk(v.List)
			walk(v.ElseList)
		case *parse.PipeNode:
			if v == nil {
				return
			}
			for _, cmd := range v.Cmds {
				walk(cmd)
			}
		case *parse.CommandNode:
			for _, arg := range v.Args {
				walk(arg)
			}
		case *parse.FieldNode:
			if len(v.Ident) > 0 {
				out = append(out, v.Ident[0])
			}
		}
	}
	walk(node)
	return out
}
