// Package profile provides a small persistence boundary for plugin-owned
// profiles.  The workflow core stores only ProfileRef values; this package
// keeps drafts, published immutable versions, and lifecycle state in the
// plugin's own PostgreSQL schema.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound       = errors.New("profile not found")
	ErrConflict       = errors.New("profile already exists")
	ErrDisabled       = errors.New("profile is disabled")
	ErrVersionMissing = errors.New("published profile version not found")
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)
var profileIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,159}$`)

type Options struct {
	// TablePrefix is a plugin-owned PostgreSQL schema name, for example
	// plugin_quant.  It is validated before being used in a table expression.
	TablePrefix string
	Validate    func(context.Context, json.RawMessage) error
	// Redact removes plugin-owned secrets before a profile is exposed through
	// list/detail responses. Resolve returns the unredacted runtime config.
	Redact    func(json.RawMessage) json.RawMessage
	Authorize func(context.Context, sdk.ProfileActor, sdk.ProfilePermission) error
}

type Draft struct {
	ID      string
	Name    string
	Summary string
	Config  json.RawMessage
}

// EnsurePublished installs an immutable, plugin-owned seed profile. It is
// intended for templates and first-run examples after the business database is
// reset. Existing user profiles are never overwritten or re-enabled.
func (s *Store) EnsurePublished(ctx context.Context, id, name, summary, version string, config json.RawMessage) error {
	if !profileIDPattern.MatchString(id) || strings.TrimSpace(name) == "" || !validVersion(version) {
		return errors.New("seed profile identity is invalid")
	}
	config = defaultConfig(config)
	if err := s.validateDraft(ctx, Draft{ID: id, Name: name, Summary: summary, Config: config}); err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row profileRow
		err := tx.Table(s.profiles).Where("id = ? AND type = ?", id, s.descriptor.Type).First(&row).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row = profileRow{ID: id, Name: strings.TrimSpace(name), Summary: strings.TrimSpace(summary), Type: s.descriptor.Type,
			Status: string(sdk.ProfileStatusActive), DraftConfigJSON: string(config), LatestPublishedVersion: version,
			CreatedBy: 0, UpdatedBy: 0, CreatedAt: now, UpdatedAt: now}
		if err := tx.Table(s.profiles).Create(&row).Error; err != nil {
			return err
		}
		return tx.Table(s.versions).Create(&versionRow{ProfileID: id, Version: version, ConfigJSON: string(config),
			Status: string(sdk.ProfileStatusPublished), PublishedAt: &now, CreatedBy: 0, CreatedAt: now}).Error
	})
}

type Store struct {
	db         *gorm.DB
	pluginID   string
	descriptor sdk.ProfileDescriptor
	profiles   string
	versions   string
	validate   func(context.Context, json.RawMessage) error
	redact     func(json.RawMessage) json.RawMessage
	authorize  func(context.Context, sdk.ProfileActor, sdk.ProfilePermission) error
}

type profileRow struct {
	ID                     string    `gorm:"primaryKey;size:160"`
	Name                   string    `gorm:"size:120;not null"`
	Summary                string    `gorm:"size:500;not null"`
	Type                   string    `gorm:"size:160;not null"`
	Status                 string    `gorm:"size:16;not null"`
	DraftConfigJSON        string    `gorm:"type:jsonb;not null"`
	LatestPublishedVersion string    `gorm:"size:128;not null"`
	CreatedBy              int64     `gorm:"not null"`
	UpdatedBy              int64     `gorm:"not null"`
	CreatedAt              time.Time `gorm:"not null"`
	UpdatedAt              time.Time `gorm:"not null"`
	DisabledAt             *time.Time
}

type versionRow struct {
	ProfileID   string `gorm:"primaryKey;size:160"`
	Version     string `gorm:"primaryKey;size:128"`
	ConfigJSON  string `gorm:"type:jsonb;not null"`
	Status      string `gorm:"size:16;not null;index"`
	PublishedAt *time.Time
	CreatedBy   int64     `gorm:"not null"`
	CreatedAt   time.Time `gorm:"not null"`
}

func NewStore(store sdk.PluginStore, descriptor sdk.ProfileDescriptor, options Options) (*Store, error) {
	if store == nil || store.DB() == nil {
		return nil, errors.New("profile store requires a plugin database")
	}
	if err := sdk.ValidateProfileDescriptor(descriptor); err != nil {
		return nil, err
	}
	prefix := options.TablePrefix
	if prefix == "" {
		prefix = "plugin_" + strings.NewReplacer(".", "_", "-", "_").Replace(store.PluginID())
	}
	if !identifierPattern.MatchString(prefix) {
		return nil, fmt.Errorf("invalid profile schema %q", prefix)
	}
	tableSuffix := strings.NewReplacer(".", "_", "-", "_").Replace(descriptor.Type)
	if len(prefix)+len(tableSuffix)+18 > 63 {
		return nil, errors.New("profile table name is too long")
	}
	s := &Store{
		db: store.DB(), pluginID: store.PluginID(), descriptor: descriptor,
		profiles: prefix + ".profiles_" + tableSuffix, versions: prefix + ".profile_versions_" + tableSuffix,
		validate: options.Validate, redact: options.Redact, authorize: options.Authorize,
	}
	if s.validate == nil {
		s.validate = func(_ context.Context, config json.RawMessage) error {
			return sdk.ValidateProfileConfig(descriptor, config)
		}
	}
	if s.redact == nil {
		s.redact = func(raw json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), raw...) }
	}
	// Profile tables are provisioned by the owning plugin's migration.  The
	// application must not create or alter plugin-owned schema during startup.
	return s, nil
}

func (s *Store) Descriptor() sdk.ProfileDescriptor { return s.descriptor }

func (s *Store) List(ctx context.Context, query sdk.ProfileListQuery) ([]sdk.ProfileSummary, error) {
	if query.Type != "" && query.Type != s.descriptor.Type {
		return []sdk.ProfileSummary{}, nil
	}
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	db := s.db.WithContext(ctx).Table(s.profiles).Where("type = ?", s.descriptor.Type)
	if !query.IncludeDisabled {
		db = db.Where("status <> ?", string(sdk.ProfileStatusDisabled))
	}
	var rows []profileRow
	if err := db.Order("name ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]sdk.ProfileSummary, 0, len(rows))
	for _, row := range rows {
		versions, err := s.listVersions(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, s.summary(row, versions))
	}
	return result, nil
}

func (s *Store) Get(ctx context.Context, id string, includeDisabled bool) (sdk.ProfileSummary, error) {
	var row profileRow
	query := s.db.WithContext(ctx).Table(s.profiles).Where("id = ? AND type = ?", id, s.descriptor.Type)
	if !includeDisabled {
		query = query.Where("status <> ?", string(sdk.ProfileStatusDisabled))
	}
	if err := query.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return sdk.ProfileSummary{}, ErrNotFound
		}
		return sdk.ProfileSummary{}, err
	}
	versions, err := s.listVersions(ctx, row.ID)
	if err != nil {
		return sdk.ProfileSummary{}, err
	}
	return s.summary(row, versions), nil
}

func (s *Store) Create(ctx context.Context, actor sdk.ProfileActor, draft Draft) (sdk.ProfileSummary, error) {
	if err := s.authorizeCall(ctx, actor, sdk.ProfilePermissionWrite); err != nil {
		return sdk.ProfileSummary{}, err
	}
	draft.Config = defaultConfig(draft.Config)
	if draft.ID == "" {
		draft.ID = uuid.NewString()
	}
	if err := s.validateDraft(ctx, draft); err != nil {
		return sdk.ProfileSummary{}, err
	}
	if !profileIDPattern.MatchString(draft.ID) || strings.TrimSpace(draft.Name) == "" {
		return sdk.ProfileSummary{}, errors.New("profile id and name are invalid")
	}
	now := time.Now().UTC()
	row := profileRow{ID: draft.ID, Name: strings.TrimSpace(draft.Name), Summary: strings.TrimSpace(draft.Summary), Type: s.descriptor.Type, Status: string(sdk.ProfileStatusDraft), DraftConfigJSON: string(draft.Config), CreatedBy: actor.UserID, UpdatedBy: actor.UserID, CreatedAt: now, UpdatedAt: now}
	if err := s.db.WithContext(ctx).Table(s.profiles).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return sdk.ProfileSummary{}, ErrConflict
		}
		return sdk.ProfileSummary{}, err
	}
	return s.summary(row, nil), nil
}

func (s *Store) Update(ctx context.Context, actor sdk.ProfileActor, draft Draft) (sdk.ProfileSummary, error) {
	if err := s.authorizeCall(ctx, actor, sdk.ProfilePermissionWrite); err != nil {
		return sdk.ProfileSummary{}, err
	}
	draft.Config = defaultConfig(draft.Config)
	if err := s.validateDraft(ctx, draft); err != nil {
		return sdk.ProfileSummary{}, err
	}
	var row profileRow
	if err := s.db.WithContext(ctx).Table(s.profiles).Where("id = ? AND type = ?", draft.ID, s.descriptor.Type).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return sdk.ProfileSummary{}, ErrNotFound
		}
		return sdk.ProfileSummary{}, err
	}
	if row.Status == string(sdk.ProfileStatusDisabled) {
		return sdk.ProfileSummary{}, ErrDisabled
	}
	row.Name, row.Summary, row.DraftConfigJSON, row.UpdatedBy, row.UpdatedAt = strings.TrimSpace(draft.Name), strings.TrimSpace(draft.Summary), string(draft.Config), actor.UserID, time.Now().UTC()
	if err := s.db.WithContext(ctx).Table(s.profiles).Save(&row).Error; err != nil {
		return sdk.ProfileSummary{}, err
	}
	versions, err := s.listVersions(ctx, row.ID)
	if err != nil {
		return sdk.ProfileSummary{}, err
	}
	return s.summary(row, versions), nil
}

func (s *Store) Copy(ctx context.Context, actor sdk.ProfileActor, sourceID string, draft Draft) (sdk.ProfileSummary, error) {
	if err := s.authorizeCall(ctx, actor, sdk.ProfilePermissionWrite); err != nil {
		return sdk.ProfileSummary{}, err
	}
	var source profileRow
	if err := s.db.WithContext(ctx).Table(s.profiles).Where("id = ? AND type = ?", sourceID, s.descriptor.Type).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return sdk.ProfileSummary{}, ErrNotFound
		}
		return sdk.ProfileSummary{}, err
	}
	if source.Status == string(sdk.ProfileStatusDisabled) {
		return sdk.ProfileSummary{}, ErrDisabled
	}
	if len(draft.Config) == 0 {
		draft.Config = json.RawMessage(source.DraftConfigJSON)
	}
	if draft.Name == "" {
		draft.Name = source.Name + " 副本"
	}
	if draft.Summary == "" {
		draft.Summary = source.Summary
	}
	if draft.ID == "" {
		draft.ID = source.ID + "-copy-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	}
	return s.Create(ctx, actor, draft)
}

// Delete removes only an unpublished draft. Published versions are immutable
// facts, so callers must use Disable when a profile has been published.
func (s *Store) Delete(ctx context.Context, actor sdk.ProfileActor, id string) error {
	if err := s.authorizeCall(ctx, actor, sdk.ProfilePermissionWrite); err != nil {
		return err
	}
	var count int64
	if err := s.db.WithContext(ctx).Table(s.versions).Where("profile_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrConflict
	}
	result := s.db.WithContext(ctx).Table(s.profiles).Where("id = ? AND type = ?", id, s.descriptor.Type).Delete(&profileRow{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Publish(ctx context.Context, actor sdk.ProfileActor, id, version string) (sdk.ProfileSummary, error) {
	if err := s.authorizeCall(ctx, actor, sdk.ProfilePermissionPublish); err != nil {
		return sdk.ProfileSummary{}, err
	}
	if !profileIDPattern.MatchString(id) || !validVersion(version) {
		return sdk.ProfileSummary{}, errors.New("profile id or version is invalid")
	}
	var result sdk.ProfileSummary
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row profileRow
		if err := tx.Table(s.profiles).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND type = ?", id, s.descriptor.Type).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if row.Status == string(sdk.ProfileStatusDisabled) {
			return ErrDisabled
		}
		if row.DraftConfigJSON == "" {
			return errors.New("profile draft is empty")
		}
		var count int64
		if err := tx.Table(s.versions).Where("profile_id = ? AND version = ?", id, version).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrConflict
		}
		now := time.Now().UTC()
		publishedRow := versionRow{ProfileID: id, Version: version, ConfigJSON: row.DraftConfigJSON, Status: string(sdk.ProfileStatusPublished), PublishedAt: &now, CreatedBy: actor.UserID, CreatedAt: now}
		if err := tx.Table(s.versions).Create(&publishedRow).Error; err != nil {
			return err
		}
		row.Status, row.LatestPublishedVersion, row.UpdatedBy, row.UpdatedAt, row.DisabledAt = string(sdk.ProfileStatusActive), version, actor.UserID, now, nil
		if err := tx.Table(s.profiles).Save(&row).Error; err != nil {
			return err
		}
		result = s.summary(row, []versionRow{publishedRow})
		return nil
	})
	if err == nil {
		var row profileRow
		if loadErr := s.db.WithContext(ctx).Table(s.profiles).Where("id = ? AND type = ?", id, s.descriptor.Type).First(&row).Error; loadErr != nil {
			return sdk.ProfileSummary{}, loadErr
		}
		versions, loadErr := s.listVersions(ctx, id)
		if loadErr != nil {
			return sdk.ProfileSummary{}, loadErr
		}
		result = s.summary(row, versions)
	}
	return result, err
}

func (s *Store) Disable(ctx context.Context, actor sdk.ProfileActor, id string) error {
	if err := s.authorizeCall(ctx, actor, sdk.ProfilePermissionDisable); err != nil {
		return err
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Table(s.profiles).Where("id = ? AND type = ?", id, s.descriptor.Type).Updates(map[string]any{"status": string(sdk.ProfileStatusDisabled), "disabled_at": &now, "updated_by": actor.UserID, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ValidateNewReference(ctx context.Context, ref sdk.ProfileRef) error {
	if err := sdk.ValidateProfileRef(ref); err != nil {
		return err
	}
	if ref.PluginID != s.pluginID || ref.Type != s.descriptor.Type {
		return errors.New("profile reference does not belong to this provider")
	}
	var row profileRow
	if err := s.db.WithContext(ctx).Table(s.profiles).Where("id = ? AND type = ?", ref.ProfileID, s.descriptor.Type).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if row.Status == string(sdk.ProfileStatusDisabled) {
		return ErrDisabled
	}
	var version versionRow
	if err := s.db.WithContext(ctx).Table(s.versions).Where("profile_id = ? AND version = ? AND status = ?", ref.ProfileID, ref.Version, string(sdk.ProfileStatusPublished)).First(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrVersionMissing
		}
		return err
	}
	return nil
}

func (s *Store) Resolve(ctx context.Context, ref sdk.ProfileRef) (sdk.ResolvedProfile, error) {
	if err := sdk.ValidateProfileRef(ref); err != nil {
		return sdk.ResolvedProfile{}, err
	}
	if ref.PluginID != s.pluginID || ref.Type != s.descriptor.Type {
		return sdk.ResolvedProfile{}, errors.New("profile reference does not belong to this provider")
	}
	var row profileRow
	if err := s.db.WithContext(ctx).Table(s.profiles).Where("id = ? AND type = ?", ref.ProfileID, s.descriptor.Type).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return sdk.ResolvedProfile{}, ErrNotFound
		}
		return sdk.ResolvedProfile{}, err
	}
	var version versionRow
	if err := s.db.WithContext(ctx).Table(s.versions).Where("profile_id = ? AND version = ? AND status = ?", ref.ProfileID, ref.Version, string(sdk.ProfileStatusPublished)).First(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return sdk.ResolvedProfile{}, ErrVersionMissing
		}
		return sdk.ResolvedProfile{}, err
	}
	return sdk.ResolvedProfile{Ref: ref, Name: row.Name, Summary: row.Summary, Config: json.RawMessage(version.ConfigJSON)}, nil
}

func (s *Store) listVersions(ctx context.Context, profileID string) ([]versionRow, error) {
	var rows []versionRow
	if err := s.db.WithContext(ctx).Table(s.versions).Where("profile_id = ?", profileID).Order("created_at DESC, version DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) summary(row profileRow, versions []versionRow) sdk.ProfileSummary {
	items := make([]sdk.ProfileVersionSummary, 0, len(versions))
	for _, version := range versions {
		items = append(items, sdk.ProfileVersionSummary{Version: version.Version, Status: sdk.ProfileStatus(version.Status), PublishedAt: version.PublishedAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	return sdk.ProfileSummary{PluginID: s.pluginID, ID: row.ID, Name: row.Name, Summary: row.Summary, Type: row.Type, Status: sdk.ProfileStatus(row.Status), LatestPublishedVersion: row.LatestPublishedVersion, Versions: items, Config: s.redact(json.RawMessage(row.DraftConfigJSON)), ConfigSchema: append(json.RawMessage(nil), s.descriptor.ConfigSchema...), UISchema: append(json.RawMessage(nil), s.descriptor.UISchema...)}
}

func (s *Store) validateDraft(ctx context.Context, draft Draft) error {
	if !json.Valid(draft.Config) {
		return errors.New("profile config must be valid JSON")
	}
	return s.validate(ctx, draft.Config)
}

func defaultConfig(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), raw...)
}

func (s *Store) authorizeCall(ctx context.Context, actor sdk.ProfileActor, permission sdk.ProfilePermission) error {
	if s.authorize == nil {
		return nil
	}
	return s.authorize(ctx, actor, permission)
}

func validVersion(version string) bool {
	version = strings.TrimSpace(version)
	return version != "" && len(version) <= 128 && !strings.ContainsAny(version, "\r\n")
}
