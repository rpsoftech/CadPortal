package export

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func TestWriteDesignsCSV(t *testing.T) {
	var buf bytes.Buffer
	err := WriteDesignsCSV(&buf, []service.ImportRow{
		{Code: "LR001", Name: "Ring, with comma", Category: "Rings", Tags: "Motif:Floral;Audience:Bridal", VolumeMM3: "200"},
		{Code: "LR001", VolumeMM3: "300"},
		{Code: "LE001", Name: "=HYPERLINK(\"http://x\")", Category: "Earrings", RefWeightG: "3.116", RefMetal: "18KY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(recs[0], ",") != strings.Join(DesignsHeader, ",") {
		t.Errorf("header = %v", recs[0])
	}
	if recs[1][1] != "Ring, with comma" || recs[1][3] != "Motif:Floral;Audience:Bridal" || recs[1][4] != "200" {
		t.Errorf("row 1 = %q", recs[1])
	}
	if recs[2][1] != "" || recs[2][4] != "300" {
		t.Errorf("second variation row = %q", recs[2])
	}
	if !strings.HasPrefix(recs[3][1], "'=") {
		t.Errorf("formula not neutralised: %q", recs[3][1])
	}
	if recs[3][5] != "3.116" || recs[3][6] != "18KY" {
		t.Errorf("reference columns = %q", recs[3])
	}
}
