package exams

import (
	"context"
	"slices"
	"strings"
)

const RegulationsPerPage = 20

type Regulation struct {
	ProgramID, Program, Degree, Version string
	Source                              *RegulationSource
}

type RegulationDirectory struct{ records []Regulation }
type RegulationFilter struct{ Query, Program, Degree, Version string }
type RegulationPage struct {
	Records      []Regulation
	Total, Pages int
}

func (s *Service) Regulations(ctx context.Context) (RegulationDirectory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pv.program_id,COALESCE(sp.name,p.name),COALESCE(pq.degree,''),pv.name
		FROM program_versions pv JOIN programs p ON p.id=pv.program_id AND p.deleted_at IS NULL
		LEFT JOIN program_qualifications pq ON pq.legacy_program_id=p.id
		LEFT JOIN study_programs sp ON sp.id=pq.study_program_id
		ORDER BY pv.name DESC,COALESCE(sp.name,p.name) COLLATE NOCASE,pq.degree,p.id`)
	if err != nil {
		return RegulationDirectory{}, err
	}
	var result RegulationDirectory
	var ids []int64
	for rows.Next() {
		var id int64
		var record Regulation
		if err := rows.Scan(&id, &record.Program, &record.Degree, &record.Version); err != nil {
			rows.Close()
			return RegulationDirectory{}, err
		}
		if record.Degree == "" {
			record.Program, record.Degree = ProgramName(record.Program)
		}
		result.records = append(result.records, record)
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return RegulationDirectory{}, err
	}
	sources, err := s.storedRegulationSources(ctx)
	if err != nil {
		return RegulationDirectory{}, err
	}
	for i := range result.records {
		r := &result.records[i]
		r.ProgramID, err = lookupID(ctx, s.db, "programs", ids[i])
		if err != nil {
			return RegulationDirectory{}, err
		}
		for j := range sources {
			source := &sources[j]
			if source.Program == r.Program && source.Degree == r.Degree && source.Version == r.Version {
				r.Source = source
				break
			}
		}
	}
	return result, nil
}

func (d RegulationDirectory) Get(program, version string) (Regulation, bool) {
	for _, record := range d.records {
		if record.ProgramID == program && record.Version == version {
			return record, true
		}
	}
	return Regulation{}, false
}

func (d RegulationDirectory) Programs() []string {
	var values []string
	for _, record := range d.records {
		values = appendUnique(values, record.Program)
	}
	slices.Sort(values)
	return values
}

func (d RegulationDirectory) Versions() []string {
	var values []string
	for _, record := range d.records {
		values = appendUnique(values, record.Version)
	}
	slices.Sort(values)
	slices.Reverse(values)
	return values
}

func (d RegulationDirectory) Filter(f RegulationFilter, number int) (RegulationPage, bool) {
	query := strings.ToLower(strings.TrimSpace(f.Query))
	var records []Regulation
	for _, record := range d.records {
		if !strings.Contains(strings.ToLower(record.Program+" "+record.Version), query) ||
			f.Program != "" && f.Program != record.Program || f.Degree != "" && f.Degree != DegreeValue(record.Degree) || f.Version != "" && f.Version != record.Version {
			continue
		}
		records = append(records, record)
	}
	page := RegulationPage{Total: len(records), Pages: max(1, (len(records)+RegulationsPerPage-1)/RegulationsPerPage)}
	if number < 1 || number > page.Pages {
		return page, false
	}
	start := (number - 1) * RegulationsPerPage
	page.Records = records[start:min(start+RegulationsPerPage, len(records))]
	return page, true
}
