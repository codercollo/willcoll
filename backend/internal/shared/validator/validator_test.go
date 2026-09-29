package validator

import (
	"testing"

	"github.com/google/uuid"
)

func TestStruct(t *testing.T) {
	type createInput struct {
		LandlordID uuid.UUID `json:"landlord_id" validate:"required"`
		Name       string    `json:"name" validate:"required,max=5"`
		Phone      string    `json:"phone" validate:"required,phone"`
		Email      *string   `json:"email" validate:"omitnil,email"`
		Slug       string    `json:"slug" validate:"required,slug"`
		Fee        float64   `json:"fee" validate:"min=0,max=100"`
	}

	type patchInput struct {
		Name   *string `json:"name" validate:"omitnil,min=1"`
		Status *string `json:"status" validate:"omitnil,oneof=vacant occupied"`
	}

	ptr := func(s string) *string { return &s }

	tests := []struct {
		name  string
		input any
		want  map[string]string
	}{
		{
			name: "valid create",
			input: createInput{
				LandlordID: uuid.New(), Name: "Kiwi", Phone: "+254712345678",
				Email: ptr("a@b.co"), Slug: "runda-arcade", Fee: 5,
			},
			want: map[string]string{},
		},
		{
			name:  "empty create",
			input: createInput{Fee: 101},
			want: map[string]string{
				"landlord_id": "must be provided",
				"name":        "must be provided",
				"phone":       "must be provided",
				"slug":        "must be provided",
				"fee":         "must be at most 100",
			},
		},
		{
			name: "bad formats",
			input: createInput{
				LandlordID: uuid.New(), Name: "Too long", Phone: "0712345678",
				Email: ptr("nope"), Slug: "Runda Arcade", Fee: -1,
			},
			want: map[string]string{
				"name":  "must not be more than 5 characters long",
				"phone": "must be a valid E.164 phone number",
				"email": "must be a valid email address",
				"slug":  "must contain only lowercase letters, digits and single hyphens",
				"fee":   "must be at least 0",
			},
		},
		{
			name:  "patch with nothing sent",
			input: patchInput{},
			want:  map[string]string{},
		},
		{
			name:  "patch with bad values",
			input: patchInput{Name: ptr(""), Status: ptr("demolished")},
			want: map[string]string{
				"name":   "must not be empty",
				"status": "must be one of: vacant, occupied",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := New()
			v.Struct(tt.input)

			if len(v.Errors) != len(tt.want) {
				t.Fatalf("errors = %v, want %v", v.Errors, tt.want)
			}
			for k, msg := range tt.want {
				if v.Errors[k] != msg {
					t.Errorf("errors[%q] = %q, want %q", k, v.Errors[k], msg)
				}
			}
		})
	}
}

func TestStructKeepsFirstError(t *testing.T) {
	v := New()
	v.AddError("name", "already taken")
	v.Struct(struct {
		Name string `json:"name" validate:"required"`
	}{})

	if v.Errors["name"] != "already taken" {
		t.Errorf("errors[name] = %q, want the earlier error kept", v.Errors["name"])
	}
}

func TestStructPanicsOnNonStruct(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic for non-struct input")
		}
	}()
	New().Struct("not a struct")
}
