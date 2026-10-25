package exams

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/fachschaftinformatik/web/internal/database"
)

const ModulesPerPage = 20

type ModuleFilter struct {
	Query, Program, Degree string
}

type ModulePage struct {
	Modules      []Module
	Total, Pages int
}

// ModuleCatalog includes modules without documents and does not load exam files
// or submissions. Each study-program association remains a separate module.
func (s *Service) ModuleCatalog(ctx context.Context) (Catalog, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Catalog{}, err
	}
	defer tx.Rollback()
	result := Catalog{modules: []Module{}}
	if err := result.loadPrograms(ctx, tx); err != nil {
		return Catalog{}, err
	}
	if err := result.loadModules(ctx, tx, nil); err != nil {
		return Catalog{}, err
	}
	result.sortPrograms()
	slices.Sort(result.degrees)
	if err := tx.Commit(); err != nil {
		return Catalog{}, err
	}
	return result, nil
}

func (c *Catalog) loadModules(ctx context.Context, db database.DBTX, versions map[int64][]string) error {
	records, err := database.New(db).ListArchiveModules(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		id, err := lookupID(ctx, db, "modules", record.ID)
		if err != nil {
			return err
		}
		module := Module{ID: id, ProgramID: c.qualifications[record.ProgramID].program, Title: record.Name, Program: record.ProgramName, StudyProgram: record.StudyProgram, Degree: record.Degree, versions: versions[record.ProgramID]}
		c.modules = append(c.modules, module)
		_, degree := module.Qualification()
		c.programs = appendUnique(c.programs, module.ProgramID)
		c.degrees = appendUnique(c.degrees, DegreeValue(degree))
	}
	return nil
}

func (c Catalog) ModulePage(filter ModuleFilter, number int) (ModulePage, bool) {
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	var records []Module
	for _, module := range c.modules {
		_, degree := module.Qualification()
		if !strings.Contains(strings.ToLower(module.Title), query) ||
			filter.Program != "" && module.ProgramID != filter.Program ||
			filter.Degree != "" && DegreeValue(degree) != filter.Degree {
			continue
		}
		records = append(records, module)
	}
	slices.SortFunc(records, func(a, b Module) int {
		for _, pair := range [][2]string{{a.Title, b.Title}, {a.StudyProgram, b.StudyProgram}, {a.Degree, b.Degree}, {a.ID, b.ID}} {
			if order := strings.Compare(strings.ToLower(pair[0]), strings.ToLower(pair[1])); order != 0 {
				return order
			}
		}
		return 0
	})
	page := ModulePage{Total: len(records), Pages: max(1, (len(records)+ModulesPerPage-1)/ModulesPerPage)}
	if number < 1 || number > page.Pages {
		return page, false
	}
	start := (number - 1) * ModulesPerPage
	page.Modules = records[start:min(start+ModulesPerPage, len(records))]
	return page, true
}
