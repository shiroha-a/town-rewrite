package townmap

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		fs      []Facility
		wantErr bool
	}{
		{"default is valid", Default(), false},
		{"empty is valid", []Facility{}, false},
		{
			"missing key",
			[]Facility{{Img: "bank", Col: 1, Row: 0}},
			true,
		},
		{
			"missing img",
			[]Facility{{Key: "bank", Col: 1, Row: 0}},
			true,
		},
		{
			"col below range",
			[]Facility{{Key: "bank", Img: "bank", Col: 0, Row: 0}},
			true,
		},
		{
			"col above range",
			[]Facility{{Key: "bank", Img: "bank", Col: Cols + 1, Row: 0}},
			true,
		},
		{
			"row below range",
			[]Facility{{Key: "bank", Img: "bank", Col: 1, Row: -1}},
			true,
		},
		{
			"row above range",
			[]Facility{{Key: "bank", Img: "bank", Col: 1, Row: Rows}},
			true,
		},
		{
			"duplicate cell",
			[]Facility{
				{Key: "bank", Img: "bank", Col: 3, Row: 2},
				{Key: "gym", Img: "gym", Col: 3, Row: 2},
			},
			true,
		},
		{
			"distinct cells ok",
			[]Facility{
				{Key: "bank", Img: "bank", Col: 3, Row: 2},
				{Key: "gym", Img: "gym", Col: 4, Row: 2},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.fs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
