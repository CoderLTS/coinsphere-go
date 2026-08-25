package service

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxWorkflowArtifactBytes = int64(1 << 30)

type WorkflowActivityView struct {
	Cursor        int64  `json:"cursor"`
	WorkflowID    int64  `json:"workflowId"`
	BatchID       int64  `json:"batchId,omitempty"`
	NodeRunID     int64  `json:"nodeRunId,omitempty"`
	EventType     string `json:"eventType"`
	Status        string `json:"status,omitempty"`
	Summary       string `json:"summary"`
	ErrorCategory string `json:"errorCategory,omitempty"`
	OccurredAt    string `json:"occurredAt"`
}

type WorkflowNodeRunView struct {
	ID             int64  `json:"id"`
	NodeInstanceID string `json:"nodeInstanceId"`
	NodeType       string `json:"nodeType"`
	NodeVersion    string `json:"nodeVersion"`
	ExecutionPool  string `json:"executionPool"`
	Attempt        int    `json:"attempt"`
	LoopIteration  int    `json:"loopIteration"`
	OperationKey   string `json:"operationKey"`
	Status         string `json:"status"`
	ErrorCategory  string `json:"errorCategory,omitempty"`
	StartedAt      string `json:"startedAt"`
	CompletedAt    string `json:"completedAt,omitempty"`
	DurationMS     *int64 `json:"durationMs,omitempty"`
}

type WorkflowArtifactView struct {
	SHA256          string `json:"sha256"`
	NodeInstanceID  string `json:"nodeInstanceId,omitempty"`
	MediaType       string `json:"mediaType"`
	Encoding        string `json:"encoding"`
	SizeBytes       int64  `json:"sizeBytes"`
	StoredSizeBytes int64  `json:"storedSizeBytes"`
	DownloadURL     string `json:"downloadUrl"`
	Verified        bool   `json:"verified,omitempty"`
}

type WorkflowBatchDetail struct {
	WorkflowBatchView
	NodeRuns   []WorkflowNodeRunView  `json:"nodeRuns"`
	Activities []WorkflowActivityView `json:"activities"`
	Artifacts  []WorkflowArtifactView `json:"artifacts"`
}

type workflowArtifactManifest struct {
	SHA256          string `json:"sha256"`
	MediaType       string `json:"mediaType"`
	Encoding        string `json:"encoding"`
	SizeBytes       int64  `json:"sizeBytes"`
	StoredSizeBytes int64  `json:"storedSizeBytes"`
}

func (a *App) ListWorkflowActivities(ctx context.Context, workflowID, after int64, limit int) ([]WorkflowActivityView, int64, error) {
	if after < 0 || limit < 1 || limit > 200 {
		return nil, after, errors.New("activity cursor or limit is invalid")
	}
	var exists int64
	if err := a.DB.WithContext(ctx).Model(&db.Workflow{}).Where("id = ?", workflowID).Count(&exists).Error; err != nil {
		return nil, after, errors.New("load workflow failed")
	}
	if exists == 0 {
		return nil, after, fmt.Errorf("%w: workflow", ErrNotFound)
	}
	var rows []db.WorkflowActivity
	query := a.DB.WithContext(ctx).Where("workflow_id = ? AND cursor > ?", workflowID, after)
	if after == 0 {
		query = query.Order("cursor DESC")
	} else {
		query = query.Order("cursor")
	}
	if err := query.Limit(limit).Find(&rows).Error; err != nil {
		return nil, after, errors.New("list workflow activity failed")
	}
	if after == 0 {
		for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
			rows[left], rows[right] = rows[right], rows[left]
		}
	}
	items := make([]WorkflowActivityView, len(rows))
	next := after
	for index, row := range rows {
		items[index] = workflowActivityView(row)
		next = row.Cursor
	}
	return items, next, nil
}

func (a *App) GetWorkflowBatchDetail(ctx context.Context, batchID int64) (WorkflowBatchDetail, error) {
	batch, err := a.GetWorkflowBatch(ctx, batchID)
	if err != nil {
		return WorkflowBatchDetail{}, err
	}
	var runs []db.WorkflowNodeRun
	if err := a.DB.WithContext(ctx).Where("batch_id = ?", batchID).Order("id").Find(&runs).Error; err != nil {
		return WorkflowBatchDetail{}, errors.New("list workflow node runs failed")
	}
	nodeRuns := make([]WorkflowNodeRunView, len(runs))
	for index, run := range runs {
		nodeRuns[index] = workflowNodeRunView(run)
	}
	var activities []db.WorkflowActivity
	if err := a.DB.WithContext(ctx).Where("batch_id = ?", batchID).Order("cursor").Find(&activities).Error; err != nil {
		return WorkflowBatchDetail{}, errors.New("list workflow batch activity failed")
	}
	activityViews := make([]WorkflowActivityView, len(activities))
	for index, activity := range activities {
		activityViews[index] = workflowActivityView(activity)
	}
	var artifacts []struct {
		NodeInstanceID  string
		SHA256          string
		MediaType       string
		Encoding        string
		SizeBytes       int64
		StoredSizeBytes int64
	}
	if err := a.DB.WithContext(ctx).Raw(`
SELECT c.node_instance_id, a.sha256, r.media_type, a.encoding, r.size_bytes, a.stored_size_bytes
FROM workflow_artifact_refs r
JOIN workflow_checkpoints c ON c.id = r.checkpoint_id
JOIN workflow_artifacts a ON a.sha256 = r.artifact_sha256
WHERE c.batch_id = ?
ORDER BY c.id, r.ordinal`, batchID).Scan(&artifacts).Error; err != nil {
		return WorkflowBatchDetail{}, errors.New("list workflow batch artifacts failed")
	}
	artifactViews := make([]WorkflowArtifactView, len(artifacts))
	for index, artifact := range artifacts {
		artifactViews[index] = WorkflowArtifactView{
			SHA256: artifact.SHA256, NodeInstanceID: artifact.NodeInstanceID,
			MediaType: artifact.MediaType, Encoding: artifact.Encoding,
			SizeBytes: artifact.SizeBytes, StoredSizeBytes: artifact.StoredSizeBytes,
			DownloadURL: "/api/v1/artifacts/" + artifact.SHA256 + "/download",
		}
	}
	return WorkflowBatchDetail{WorkflowBatchView: batch, NodeRuns: nodeRuns, Activities: activityViews, Artifacts: artifactViews}, nil
}

func (a *App) GetWorkflowArtifactManifest(ctx context.Context, digest string, verify bool) (WorkflowArtifactView, error) {
	artifact, err := a.loadWorkflowArtifact(ctx, digest)
	if err != nil {
		return WorkflowArtifactView{}, err
	}
	view := WorkflowArtifactView{
		SHA256: artifact.SHA256, MediaType: artifact.MediaType, Encoding: artifact.Encoding,
		SizeBytes: artifact.SizeBytes, StoredSizeBytes: artifact.StoredSizeBytes,
		DownloadURL: "/api/v1/artifacts/" + artifact.SHA256 + "/download",
	}
	if verify {
		reader, err := a.openWorkflowArtifact(artifact)
		if err != nil {
			return WorkflowArtifactView{}, err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, reader)
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil || size != artifact.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
			return WorkflowArtifactView{}, errors.New("workflow artifact verification failed")
		}
		view.Verified = true
	}
	return view, nil
}

func (a *App) OpenWorkflowArtifact(ctx context.Context, digest string) (io.ReadCloser, WorkflowArtifactView, error) {
	artifact, err := a.loadWorkflowArtifact(ctx, digest)
	if err != nil {
		return nil, WorkflowArtifactView{}, err
	}
	reader, err := a.openWorkflowArtifact(artifact)
	if err != nil {
		return nil, WorkflowArtifactView{}, err
	}
	return reader, WorkflowArtifactView{
		SHA256: artifact.SHA256, MediaType: artifact.MediaType, Encoding: artifact.Encoding,
		SizeBytes: artifact.SizeBytes, StoredSizeBytes: artifact.StoredSizeBytes,
	}, nil
}

func (a *App) cleanupWorkflowHistory(ctx context.Context, now time.Time) error {
	if strings.TrimSpace(a.ArtifactRoot) == "" {
		return errors.New("workflow artifact storage is unavailable")
	}
	var storageKeys []string
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		eligible := `
SELECT eb.id FROM execution_batches eb
JOIN workflows w ON w.id = eb.workflow_id
WHERE eb.status IN ('succeeded','failed','cancelled')
  AND eb.completed_at < CAST(? AS TIMESTAMPTZ) - make_interval(days => w.retention_days)`
		var candidateDigests []string
		if err := tx.Raw(`SELECT DISTINCT r.artifact_sha256 FROM workflow_artifact_refs r JOIN workflow_checkpoints c ON c.id = r.checkpoint_id WHERE c.batch_id IN (`+eligible+`)`, now).Scan(&candidateDigests).Error; err != nil {
			return err
		}
		statements := []string{
			`DELETE FROM workflow_activities WHERE batch_id IN (` + eligible + `)`,
			`DELETE FROM workflow_artifact_refs WHERE checkpoint_id IN (SELECT id FROM workflow_checkpoints WHERE batch_id IN (` + eligible + `))`,
			`DELETE FROM workflow_checkpoints WHERE batch_id IN (` + eligible + `)`,
			`DELETE FROM workflow_node_runs WHERE batch_id IN (` + eligible + `)`,
			`DELETE FROM execution_batches WHERE id IN (` + eligible + `)`,
		}
		for _, statement := range statements {
			if err := tx.Exec(statement, now).Error; err != nil {
				return err
			}
		}
		var artifacts []db.WorkflowArtifact
		query := tx.Where("created_at < ?", now.Add(-30*24*time.Hour))
		if len(candidateDigests) > 0 {
			query = tx.Where("sha256 IN ? OR created_at < ?", candidateDigests, now.Add(-30*24*time.Hour))
		}
		if err := query.Find(&artifacts).Error; err != nil {
			return err
		}
		for _, artifact := range artifacts {
			var references int64
			if err := tx.Model(&db.WorkflowArtifactRef{}).Where("artifact_sha256 = ?", artifact.SHA256).Count(&references).Error; err != nil {
				return err
			}
			if references == 0 {
				if err := tx.Delete(&artifact).Error; err != nil {
					return err
				}
				storageKeys = append(storageKeys, artifact.StorageKey)
			}
		}
		return nil
	})
	if err != nil {
		return errors.New("clean workflow history failed")
	}
	for _, key := range storageKeys {
		if err := os.Remove(filepath.Join(a.ArtifactRoot, filepath.FromSlash(key))); err != nil && !errors.Is(err, os.ErrNotExist) {
			// ponytail: an orphaned compressed file is harmless; add a filesystem reconciliation sweep if this occurs in practice.
			return errors.New("remove expired workflow artifact failed")
		}
	}
	return nil
}

func workflowActivityView(row db.WorkflowActivity) WorkflowActivityView {
	view := WorkflowActivityView{
		Cursor: row.Cursor, WorkflowID: row.WorkflowID, EventType: row.EventType,
		Summary: row.Summary, OccurredAt: formatWorkflowTime(row.OccurredAt),
	}
	if row.BatchID != nil {
		view.BatchID = *row.BatchID
	}
	if row.NodeRunID != nil {
		view.NodeRunID = *row.NodeRunID
	}
	if row.Status != nil {
		view.Status = *row.Status
	}
	if row.ErrorCategory != nil {
		view.ErrorCategory = *row.ErrorCategory
	}
	return view
}

func workflowNodeRunView(run db.WorkflowNodeRun) WorkflowNodeRunView {
	view := WorkflowNodeRunView{
		ID: run.ID, NodeInstanceID: run.NodeInstanceID, NodeType: run.NodeType,
		NodeVersion: run.NodeVersion, ExecutionPool: run.ExecutionPool, Attempt: run.Attempt,
		LoopIteration: run.LoopIteration, OperationKey: run.OperationKey, Status: run.Status,
		StartedAt: formatWorkflowTime(run.StartedAt), DurationMS: run.DurationMS,
	}
	if run.ErrorCategory != nil {
		view.ErrorCategory = *run.ErrorCategory
	}
	if run.CompletedAt != nil {
		view.CompletedAt = formatWorkflowTime(*run.CompletedAt)
	}
	return view
}

func loadWorkflowArtifactManifests(tx *gorm.DB, artifacts []sdk.Artifact) ([]workflowArtifactManifest, error) {
	manifests := make([]workflowArtifactManifest, len(artifacts))
	for index, item := range artifacts {
		if !validWorkflowArtifactDigest(item.SHA256) || item.Size < 0 || strings.TrimSpace(item.MediaType) == "" {
			return nil, errors.New("workflow action returned an invalid artifact")
		}
		var artifact db.WorkflowArtifact
		if err := tx.First(&artifact, "sha256 = ?", item.SHA256).Error; err != nil {
			return nil, errors.New("workflow action returned an unknown artifact")
		}
		if artifact.SizeBytes != item.Size || artifact.MediaType != item.MediaType {
			return nil, errors.New("workflow action returned inconsistent artifact metadata")
		}
		manifests[index] = workflowArtifactManifest{
			SHA256: artifact.SHA256, MediaType: artifact.MediaType, Encoding: artifact.Encoding,
			SizeBytes: artifact.SizeBytes, StoredSizeBytes: artifact.StoredSizeBytes,
		}
	}
	return manifests, nil
}

func createWorkflowArtifactRefs(tx *gorm.DB, checkpointID int64, manifests []workflowArtifactManifest) error {
	for index, manifest := range manifests {
		if err := tx.Create(&db.WorkflowArtifactRef{
			CheckpointID: checkpointID, ArtifactSHA256: manifest.SHA256, Ordinal: index,
			MediaType: manifest.MediaType, SizeBytes: manifest.SizeBytes,
		}).Error; err != nil {
			return errors.New("create workflow artifact reference failed")
		}
	}
	return nil
}

type workflowArtifactStore struct{ app *App }

func (s workflowArtifactStore) Put(ctx context.Context, mediaType string, source io.Reader) (sdk.Artifact, error) {
	if strings.TrimSpace(s.app.ArtifactRoot) == "" {
		return sdk.Artifact{}, errors.New("workflow artifact storage is unavailable")
	}
	baseType, parameters, err := mime.ParseMediaType(strings.TrimSpace(mediaType))
	if err != nil || baseType == "" {
		return sdk.Artifact{}, errors.New("workflow artifact media type is invalid")
	}
	mediaType = mime.FormatMediaType(baseType, parameters)
	digest, key, size, storedSize, err := writeWorkflowArtifact(ctx, s.app.ArtifactRoot, source)
	if err != nil {
		return sdk.Artifact{}, err
	}
	now := time.Now().UTC()
	artifact := db.WorkflowArtifact{
		SHA256: digest, MediaType: mediaType, Encoding: "gzip", SizeBytes: size,
		StoredSizeBytes: storedSize, StorageKey: key, CreatedAt: now,
	}
	if err := s.app.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&artifact).Error; err != nil {
		return sdk.Artifact{}, errors.New("record workflow artifact failed")
	}
	var stored db.WorkflowArtifact
	if err := s.app.DB.WithContext(ctx).First(&stored, "sha256 = ?", digest).Error; err != nil {
		return sdk.Artifact{}, errors.New("load workflow artifact failed")
	}
	if stored.MediaType != mediaType || stored.SizeBytes != size || stored.StoredSizeBytes != storedSize || stored.StorageKey != key {
		return sdk.Artifact{}, errors.New("workflow artifact metadata conflicts with existing content")
	}
	return sdk.Artifact{SHA256: digest, MediaType: mediaType, Size: size}, nil
}

func (s workflowArtifactStore) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	artifact, err := s.app.loadWorkflowArtifact(ctx, digest)
	if err != nil {
		return nil, err
	}
	return s.app.openWorkflowArtifact(artifact)
}

func (a *App) loadWorkflowArtifact(ctx context.Context, digest string) (db.WorkflowArtifact, error) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !validWorkflowArtifactDigest(digest) {
		return db.WorkflowArtifact{}, fmt.Errorf("%w: workflow artifact", ErrNotFound)
	}
	var artifact db.WorkflowArtifact
	if err := a.DB.WithContext(ctx).First(&artifact, "sha256 = ?", digest).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.WorkflowArtifact{}, fmt.Errorf("%w: workflow artifact", ErrNotFound)
		}
		return db.WorkflowArtifact{}, errors.New("load workflow artifact failed")
	}
	if artifact.StorageKey != workflowArtifactKey(digest) {
		return db.WorkflowArtifact{}, errors.New("workflow artifact storage key is invalid")
	}
	return artifact, nil
}

func (a *App) openWorkflowArtifact(artifact db.WorkflowArtifact) (io.ReadCloser, error) {
	if strings.TrimSpace(a.ArtifactRoot) == "" {
		return nil, errors.New("workflow artifact storage is unavailable")
	}
	file, err := os.Open(filepath.Join(a.ArtifactRoot, filepath.FromSlash(artifact.StorageKey)))
	if err != nil {
		return nil, errors.New("open workflow artifact failed")
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, errors.New("decompress workflow artifact failed")
	}
	return &workflowArtifactReader{Reader: reader, compressed: file}, nil
}

type workflowArtifactReader struct {
	*gzip.Reader
	compressed *os.File
}

func (r *workflowArtifactReader) Close() error {
	return errors.Join(r.Reader.Close(), r.compressed.Close())
}

func writeWorkflowArtifact(ctx context.Context, root string, source io.Reader) (digest, key string, size, storedSize int64, err error) {
	if source == nil {
		return "", "", 0, 0, errors.New("workflow artifact source is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", 0, 0, errors.New("create workflow artifact directory failed")
	}
	temporary, err := os.CreateTemp(root, "artifact-*.tmp")
	if err != nil {
		return "", "", 0, 0, errors.New("create workflow artifact temporary file failed")
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	compressed := gzip.NewWriter(temporary)
	compressed.Header.ModTime = time.Unix(0, 0)
	hash := sha256.New()
	size, err = io.Copy(io.MultiWriter(compressed, hash), io.LimitReader(contextArtifactReader{ctx: ctx, source: source}, maxWorkflowArtifactBytes+1))
	if closeErr := compressed.Close(); err == nil {
		err = closeErr
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", "", 0, 0, errors.New("write workflow artifact failed")
	}
	if size > maxWorkflowArtifactBytes {
		return "", "", 0, 0, errors.New("workflow artifact exceeds 1 GiB")
	}
	digest = hex.EncodeToString(hash.Sum(nil))
	key = workflowArtifactKey(digest)
	target := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", "", 0, 0, errors.New("create workflow artifact shard failed")
	}
	if info, statErr := os.Stat(target); statErr == nil {
		return digest, key, size, info.Size(), nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", 0, 0, errors.New("inspect workflow artifact target failed")
	}
	info, err := os.Stat(temporaryName)
	if err != nil {
		return "", "", 0, 0, errors.New("inspect workflow artifact failed")
	}
	storedSize = info.Size()
	if err := os.Rename(temporaryName, target); err != nil {
		info, statErr := os.Stat(target)
		if statErr != nil {
			return "", "", 0, 0, errors.New("store workflow artifact failed")
		}
		storedSize = info.Size()
	}
	return digest, key, size, storedSize, nil
}

type contextArtifactReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextArtifactReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.source.Read(buffer)
	}
}

func validWorkflowArtifactDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func workflowArtifactKey(digest string) string { return digest[:2] + "/" + digest + ".gz" }

func marshalWorkflowArtifactManifests(manifests []workflowArtifactManifest) (string, error) {
	if manifests == nil {
		manifests = []workflowArtifactManifest{}
	}
	raw, err := json.Marshal(manifests)
	return string(raw), err
}
