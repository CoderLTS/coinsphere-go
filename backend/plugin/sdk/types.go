// Package sdk defines the public compile-time plugin contract.
package sdk

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/shopspring/decimal"
)

type NodeKind string
type ExecutionPool string
type SideEffectClass string
type StateMode string

const (
	NodeKindAction  NodeKind = "action"
	NodeKindTrigger NodeKind = "trigger"

	PoolStream  ExecutionPool = "stream"
	PoolCompute ExecutionPool = "compute"

	SideEffectNone         SideEffectClass = "none"
	SideEffectData         SideEffectClass = "data"
	SideEffectNotification SideEffectClass = "notification"
	SideEffectHumanAction  SideEffectClass = "human_action"
	SideEffectPaper        SideEffectClass = "paper"

	StateStateless  StateMode = "stateless"
	StatePersistent StateMode = "persistent"
)

type NodeDescriptor struct {
	Type         string
	Version      string
	Kind         NodeKind
	Branches     []string
	ConfigSchema json.RawMessage
	UISchema     json.RawMessage
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Pool         ExecutionPool
	SideEffect   SideEffectClass
	State        StateMode
}

type RevisionRef struct {
	WorkflowID string
	RevisionID string
}

type ActionRequest struct {
	Revision       RevisionRef
	NodeInstanceID string
	OperationKey   string
	Input          json.RawMessage
	Config         json.RawMessage
	Secrets        SecretReader
	State          StateStore
	Artifacts      ArtifactStore
	Logger         *slog.Logger
}

type ActionResult struct {
	Output    json.RawMessage
	Artifacts []Artifact
}

type ActionHandler interface {
	Execute(context.Context, ActionRequest) (ActionResult, error)
}

type TriggerRequest struct {
	Revision       RevisionRef
	NodeInstanceID string
	Config         json.RawMessage
	Secrets        SecretReader
	State          StateStore
	Logger         *slog.Logger
}

type TriggerHandler interface {
	Run(context.Context, TriggerRequest, Emitter) error
}

type Emitter interface {
	Emit(context.Context, cloudevents.Event) error
}

type SecretReader interface {
	Read(context.Context, string) ([]byte, error)
}

type StateStore interface {
	Load(context.Context) (json.RawMessage, error)
	Save(context.Context, json.RawMessage) error
}

type Artifact struct {
	SHA256    string
	MediaType string
	Size      int64
}

type ArtifactStore interface {
	Put(context.Context, string, io.Reader) (Artifact, error)
	Open(context.Context, string) (io.ReadCloser, error)
}

type ResultPageDescriptor struct {
	PageKey        string
	Title          string
	ComponentEntry string
	ScopeSchema    json.RawMessage
	FilterSchema   json.RawMessage
	Actions        []string
	Mobile         bool
}

type PageDescriptor struct {
	PageKey   string
	Title     string
	Icon      string
	KeepAlive bool
}

type RegisteredPage struct {
	PluginID string
	PageDescriptor
}

type ScopeKind string

const (
	ScopeWorkflow ScopeKind = "workflow"
	ScopeResult   ScopeKind = "result"
	ScopeSystem   ScopeKind = "system"
)

type RouteScope interface{ routeScope() }

type WorkflowScope struct {
	PluginID       string
	WorkflowID     string
	NodeInstanceID string
}

func (WorkflowScope) routeScope() {}

type ResultScope struct {
	ViewID         string
	PluginID       string
	PageKey        string
	Scope          json.RawMessage
	Filters        json.RawMessage
	AllowedActions []string
	UserID         int64
	RoleCodes      []string
	HumanTasks     HumanTaskService
}

func (ResultScope) routeScope() {}

type HumanTaskService interface {
	Decide(context.Context, int64, string, int64) error
}

type SystemScope struct {
	PluginID  string
	UserID    int64
	RoleCodes []string
}

func (SystemScope) routeScope() {}

type ScopedRouteHandler func(http.ResponseWriter, *http.Request, RouteScope)

type RouteDescriptor struct {
	Method  string
	Pattern string
	Scope   ScopeKind
	Action  string
}

type RegisteredRoute struct {
	PluginID   string
	Descriptor RouteDescriptor
	Handler    ScopedRouteHandler
}

type Candle struct {
	OpenTime  time.Time
	CloseTime time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
}

type EvaluateRequest struct {
	Market      string
	Instrument  string
	Interval    string
	Candles     []Candle
	Parameters  json.RawMessage
	EvaluatedAt time.Time
}

type StrategyDescriptor struct {
	ID              string
	Version         string
	Name            string
	ParameterSchema json.RawMessage
	MinimumLookback int
}

type Strategy interface {
	Descriptor() StrategyDescriptor
	Evaluate(context.Context, EvaluateRequest) (decimal.Decimal, error)
}
