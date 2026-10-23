package service

import (
	"context"
	"errors"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
)

func TestJobBagQRText(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR054")

	jb, err := s.JobBag(ctx, vid, "18KR", "2HD")
	if err != nil {
		t.Fatal(err)
	}
	if jb.QR != "LR054-V1" {
		t.Errorf("without a public address the QR holds the variation code, got %q", jb.QR)
	}
	s.AppBaseURL = "https://cad.example.com"
	jb, _ = s.JobBag(ctx, vid, "18KR", "2HD")
	if want := "https://cad.example.com/designs/LR054?v=LR054-V1"; jb.QR != want {
		t.Errorf("QR = %q, want %q", jb.QR, want)
	}
}

func TestPlanJobBags(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := fixtureVariation(t, s, "LR055")
	b := fixtureVariation(t, s, "LR056")
	bad := fixtureVariation(t, s, "LR057")
	if _, err := s.SetSeats(ctx, bad, append(fixtureSeats(), SeatInput{SizeMM: d("1.50"), StoneCount: 2})); err != nil {
		t.Fatal(err)
	}
	base := JobBagsRequest{MetalCode: "18KR", StoneCode: "2HD"}

	// Order is the request order; duplicates are kept; a per-item metal wins over the default.
	req := base
	req.Items = []JobBagItem{{VariationID: b}, {VariationID: a, MetalCode: "22KY"}, {VariationID: b}}
	bags, skipped, err := s.PlanJobBags(ctx, req)
	if err != nil || len(skipped) != 0 {
		t.Fatal(err, skipped)
	}
	got := []string{bags[0].VariationCode, bags[1].VariationCode, bags[2].VariationCode}
	if got[0] != "LR056-V1" || got[1] != "LR055-V1" || got[2] != "LR056-V1" || bags[1].MetalCode != "22KY" || bags[0].MetalCode != "18KR" {
		t.Errorf("order/metal: %v", got)
	}
	if bags[1].NetG.StringFixed(3) != "3.550" {
		t.Errorf("22KY net = %s", bags[1].NetG)
	}

	// design_ids come after the items and are not repeated.
	req = base
	req.Items = []JobBagItem{{VariationID: b}}
	d, _ := s.Q.GetVariation(ctx, a)
	req.DesignIDs = []int64{d.DesignID, d.DesignID}
	bags, _, err = s.PlanJobBags(ctx, req)
	if err != nil || len(bags) != 2 || bags[0].VariationCode != "LR056-V1" || bags[1].VariationCode != "LR055-V1" {
		t.Fatalf("designs: %v %d", err, len(bags))
	}

	// Every failing bag is listed, none is silently printed.
	req = base
	req.Items = []JobBagItem{{VariationID: a}, {VariationID: bad}, {VariationID: bad}}
	var jbe *JobBagsError
	if _, _, err = s.PlanJobBags(ctx, req); !errors.As(err, &jbe) || len(jbe.Failures) != 2 ||
		jbe.Failures[0].VariationCode != "LR057-V1" || jbe.Failures[0].DesignCode != "LR057" ||
		len(jbe.Failures[0].Missing) != 1 || jbe.Failures[0].Missing[0] != (calc.MissingPair{SizeMM: "1.50", Code: "2HD"}) {
		t.Fatalf("failures: %v", err)
	}
	req.SkipIncomplete = true
	bags, skipped, err = s.PlanJobBags(ctx, req)
	if err != nil || len(bags) != 1 || len(skipped) != 2 {
		t.Fatalf("skip: %v %d %d", err, len(bags), len(skipped))
	}

	// Request errors.
	for name, r := range map[string]JobBagsRequest{
		"nothing":      base,
		"no metal":     {StoneCode: "2HD", Items: []JobBagItem{{VariationID: a}}},
		"bad metal":    {MetalCode: "99K", StoneCode: "2HD", Items: []JobBagItem{{VariationID: a}}},
		"bad stone":    {MetalCode: "18KR", StoneCode: "9ZZ", Items: []JobBagItem{{VariationID: a}}},
		"no id":        {MetalCode: "18KR", StoneCode: "2HD", Items: []JobBagItem{{}}},
		"one too many": {MetalCode: "18KR", StoneCode: "2HD", Items: make([]JobBagItem, MaxJobBags+1)},
	} {
		if _, _, err := s.PlanJobBags(ctx, r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPlanJobBagsIgnoresDeletedVariations(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := fixtureVariation(t, s, "LR058")
	v, _ := s.Q.GetVariation(ctx, a)
	second, err := s.CreateVariation(ctx, v.DesignID, BaseInput{VolumeMM3: dp("100")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVariation(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	req := JobBagsRequest{MetalCode: "18KR", StoneCode: "2HD", DesignIDs: []int64{v.DesignID}}
	bags, _, err := s.PlanJobBags(ctx, req)
	if err != nil || len(bags) != 1 || bags[0].VariationCode != "LR058-V1" {
		t.Fatalf("design_ids must skip the deleted variation: %v %d", err, len(bags))
	}
	req = JobBagsRequest{MetalCode: "18KR", StoneCode: "2HD", Items: []JobBagItem{{VariationID: second.ID}}}
	if _, _, err := s.PlanJobBags(ctx, req); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deleted variation asked for by id: %v", err)
	}
}
