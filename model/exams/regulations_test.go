package exams

import (
	"context"
	"strings"
	"testing"
)

func TestRegulationSourcesRespectQualificationAndArchiveYear(t *testing.T) {
	s, db := examsService(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO programs(id,name) VALUES(3,'Informatik und Design (B. Sc.)');
		INSERT INTO program_versions(program_id,name) VALUES(3,'2023')`); err != nil {
		t.Fatal(err)
	}
	seedPublicIDs(t, db)
	directory, err := s.Regulations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page, ok := directory.Filter(RegulationFilter{}, 1)
	if !ok || page.Total != 4 {
		t.Fatalf("PO list: %+v", page)
	}
	for _, record := range page.Records {
		if record.Version == "2017" {
			if record.Source != nil {
				t.Fatal("archive label 2017 incorrectly mapped to another PO")
			}
			continue
		}
		if record.Source == nil {
			t.Fatalf("source missing: %+v", record)
		}
		if record.Program == "Informatik und Design" {
			if record.Version != "2023" || record.Source.PO.Date != "2025-01-08" || record.Source.Note == "" {
				t.Fatal("WHS year discrepancy lost")
			}
			continue
		}
		if record.Degree == "Bachelor" && !strings.HasSuffix(record.Source.PO.URL, "BPO_IN_2023.pdf") {
			t.Fatal("incorrect Bachelor PO")
		}
		if record.Degree == "Master" && !strings.HasSuffix(record.Source.PO.URL, "MPO_IN_2023.pdf") {
			t.Fatal("incorrect Master PO")
		}
	}
	if record, ok := directory.Get(testPublicID(t, db, "programs", "3"), "2025"); ok {
		t.Fatalf("invented archive year: %+v", record)
	}
	master, ok := directory.Filter(RegulationFilter{Degree: "master", Version: "2023"}, 1)
	if !ok || master.Total != 1 || master.Records[0].Degree != "Master" {
		t.Fatal("degree and year filters did not intersect")
	}
}
