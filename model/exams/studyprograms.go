package exams

import (
	"context"
	"slices"
	"strings"
)

const StudyProgramsPerPage = 20

type StudyProgram struct {
	Name    string
	Degrees []string
	Modules int
}

type StudyProgramDirectory struct{ programs []StudyProgram }

type StudyProgramPage struct {
	Programs     []StudyProgram
	Total, Pages int
}

// StudyPrograms groups qualifications under their shared study program, including
// programs without modules. Deleted qualifications and modules do not contribute.
func (s *Service) StudyPrograms(ctx context.Context) (StudyProgramDirectory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sp.id,sp.name,COALESCE(pq.degree,''),COUNT(m.id)
		FROM study_programs sp
		LEFT JOIN program_qualifications pq ON pq.study_program_id=sp.id
			AND EXISTS(SELECT 1 FROM programs p WHERE p.id=pq.legacy_program_id AND p.deleted_at IS NULL)
		LEFT JOIN modules m ON m.program_id=pq.legacy_program_id AND m.deleted_at IS NULL
		GROUP BY sp.id,sp.name,pq.degree ORDER BY sp.name COLLATE NOCASE,sp.id,pq.degree`)
	if err != nil {
		return StudyProgramDirectory{}, err
	}
	defer rows.Close()
	result := StudyProgramDirectory{}
	indices := map[int64]int{}
	for rows.Next() {
		var id int64
		var name, degree string
		var count int
		if err := rows.Scan(&id, &name, &degree, &count); err != nil {
			return StudyProgramDirectory{}, err
		}
		index, exists := indices[id]
		if !exists {
			index = len(result.programs)
			indices[id] = index
			result.programs = append(result.programs, StudyProgram{Name: name})
		}
		program := &result.programs[index]
		program.Degrees = appendUnique(program.Degrees, DegreeValue(degree))
		program.Modules += count
	}
	return result, rows.Err()
}

func (d StudyProgramDirectory) Filter(query, degree string, number int) (StudyProgramPage, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	var records []StudyProgram
	for _, program := range d.programs {
		if strings.Contains(strings.ToLower(program.Name), query) && (degree == "" || slices.Contains(program.Degrees, degree)) {
			records = append(records, program)
		}
	}
	page := StudyProgramPage{Total: len(records), Pages: max(1, (len(records)+StudyProgramsPerPage-1)/StudyProgramsPerPage)}
	if number < 1 || number > page.Pages {
		return page, false
	}
	start := (number - 1) * StudyProgramsPerPage
	page.Programs = records[start:min(start+StudyProgramsPerPage, len(records))]
	return page, true
}
