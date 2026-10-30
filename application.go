package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/fachschaftinformatik/web/internal/audit"
	"github.com/fachschaftinformatik/web/internal/config"
	"github.com/fachschaftinformatik/web/internal/database"
	"github.com/fachschaftinformatik/web/internal/email"
	"github.com/fachschaftinformatik/web/model/events"
	"github.com/fachschaftinformatik/web/model/exams"
	"github.com/fachschaftinformatik/web/model/media"
	"github.com/fachschaftinformatik/web/model/meetings"
	"github.com/fachschaftinformatik/web/model/members"
	"github.com/fachschaftinformatik/web/model/organization"
	"github.com/fachschaftinformatik/web/model/posts"
	"github.com/fachschaftinformatik/web/model/users"
	"github.com/fachschaftinformatik/web/site"
	"github.com/fachschaftinformatik/web/storage"
	"github.com/fachschaftinformatik/web/storage/disk"
	"github.com/fachschaftinformatik/web/storage/s3"
)

type application struct {
	handler http.Handler
	db      *sql.DB
	exams   *exams.Service
	local   *disk.Store
	cancel  context.CancelFunc
	done    chan struct{}
}

func newApplication(ctx context.Context, databasePath, blobPath string) (_ *application, err error) {
	cfg := config.New()
	if err := cfg.ValidateDomains(); err != nil {
		return nil, err
	}
	origin, err := url.Parse(cfg.Domain)
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("DOMAIN must be an absolute HTTP or HTTPS URL")
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0700); err != nil {
		return nil, err
	}
	if err := database.Migrate(databasePath, log.Default()); err != nil {
		return nil, err
	}
	db, err := database.NewConnection(databasePath)
	if err != nil {
		return nil, err
	}
	app := &application{db: db}
	defer func() {
		if err != nil {
			err = errors.Join(err, app.Close())
		}
	}()
	var objects storage.Provider
	if blobPath != "" || cfg.S3Endpoint == "" {
		if blobPath == "" {
			blobPath = filepath.Join(filepath.Dir(databasePath), "blobs")
		}
		app.local, err = disk.New(blobPath)
		if err != nil {
			return nil, err
		}
		objects = app.local
	} else {
		objects, err = s3.NewClient(cfg)
		if err != nil {
			return nil, err
		}
	}
	team, err := members.Open(ctx, db)
	if err != nil {
		return nil, err
	}
	publications, err := meetings.NewStore(db, objects)
	if err != nil {
		return nil, err
	}
	org, err := organization.NewStore(ctx, db)
	if err != nil {
		return nil, err
	}
	identity := users.NewService(db, cfg.Domain, users.DomainPolicy{Registration: cfg.RegistrationDomains, Accounts: cfg.AccountDomains})
	examDocuments := exams.NewService(db, objects)
	app.exams = examDocuments
	buildVersion := Version
	if cfg.AppVersion != "" {
		buildVersion = cfg.AppVersion
	}
	auditStore := audit.NewStore(db)
	app.handler, err = site.New(site.Services{
		FeedbackMail:  email.NewSender(cfg, org),
		Audit:         auditStore,
		BuildVersion:  buildVersion,
		BuildRevision: Revision,
		Users:         identity, Exams: examDocuments, Members: team, Meetings: publications, Organization: org,
		Posts: posts.NewService(db), Media: media.NewService(db, objects), Events: events.NewService(db, objects),
	}, origin.Scheme == "https")
	if err != nil {
		return nil, err
	}
	router := app.handler
	app.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r.WithContext(audit.WithRecorder(r.Context(), auditStore)))
	})
	maintenanceCtx, cancel := context.WithCancel(audit.WithRecorder(ctx, auditStore))
	app.cancel, app.done = cancel, make(chan struct{})
	go func() {
		defer close(app.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		sender := email.NewSender(cfg, org)
		for {
			if err := examDocuments.CleanupBlobs(maintenanceCtx); err != nil && maintenanceCtx.Err() == nil {
				log.Printf("Exam document maintenance: %v", err)
			}
			if err := publications.CleanupBlobs(maintenanceCtx); err != nil && maintenanceCtx.Err() == nil {
				log.Printf("Meeting file maintenance: %v", err)
			}
			var err error
			if cfg.SMTPHost != "" {
				err = identity.DeliverMail(maintenanceCtx, sender)
				if err := examDocuments.DeliverSubmissionMail(maintenanceCtx, sender); err != nil && maintenanceCtx.Err() == nil {
					log.Printf("Submission mail maintenance: %v", err)
				}
				if err := examDocuments.DeliverSubmissionDecisionMail(maintenanceCtx, sender); err != nil && maintenanceCtx.Err() == nil {
					log.Printf("Submission decision mail maintenance: %v", err)
				}
			} else {
				err = identity.PurgeExpired(maintenanceCtx)
			}
			if err != nil && maintenanceCtx.Err() == nil {
				log.Printf("Account maintenance: %v", err)
			}
			select {
			case <-maintenanceCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return app, nil
}

func (a *application) Close() error {
	if a.cancel != nil {
		a.cancel()
		<-a.done
	}
	var err error
	if a.local != nil {
		err = a.local.Close()
	}
	return errors.Join(err, a.db.Close())
}
