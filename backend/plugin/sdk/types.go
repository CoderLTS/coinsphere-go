// Package sdk defines the public compile-time plugin contract.
package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type NodeKind string
type ExecutionPool string
type SideEffectClass string
type StateMode string

type NodeCapabilities struct {
	Deterministic bool `json:"deterministic"`
	Stateless     bool `json:"stateless"`
}

const (
	NodeKindAction  NodeKind = "action"
	NodeKindTrigger NodeKind = "trigger"

	PoolStream  ExecutionPool = "stream"
	PoolCompute ExecutionPool = "compute"

	SideEffectExternal     SideEffectClass = "external"
	SideEffectNone         SideEffectClass = "none"
	SideEffectData         SideEffectClass = "data"
	SideEffectNotification SideEffectClass = "notification"
	SideEffectHumanAction  SideEffectClass = "human_action"

	StateStateless  StateMode = "stateless"
	StatePersistent StateMode = "persistent"
)

type NodeDescriptor struct {
	EditorKey            string
	ExecutionPermissions []string

	Type           string
	Version        string
	Kind           NodeKind
	Title          string
	Description    string
	Category       string
	Aliases        []string
	Tags           []string
	SortOrder      int
	Color          string
	Icon           string
	Width          int
	Height         int
	Capabilities   NodeCapabilities
	Branches       []string
	ConfigSchema   json.RawMessage
	UISchema       json.RawMessage
	InputSchema    json.RawMessage
	OutputSchema   json.RawMessage
	Pool           ExecutionPool
	SideEffect     SideEffectClass
	RetrySafe      bool
	State          StateMode
	ValidateConfig func(json.RawMessage) error
}

type PluginMenuMode string

const (
	PluginMenuOwn      PluginMenuMode = "own"
	PluginMenuExisting PluginMenuMode = "existing"
	PluginMenuDirect   PluginMenuMode = "direct"
)

type PluginMenuDescriptor struct {
	Mode   PluginMenuMode
	Title  string
	Icon   string
	Parent string
}

type RevisionRef struct {
	WorkflowID string
	RevisionID string
}

type ActionRequest struct {
	GraphSnapshot json.RawMessage

	Revision       RevisionRef
	NodeInstanceID string
	OperationKey   string
	Input          json.RawMessage
	Config         json.RawMessage
	Secrets        SecretReader
	State          StateStore
	Artifacts      ArtifactStore
	Incoming       []NodeOutput
	Logger         *slog.Logger
}

type NodeOutput struct {
	NodeInstanceID string
	SourcePort     string
	Output         json.RawMessage
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

type PermissionDescriptor struct {
	Code      string `json:"code"`
	Title     string `json:"title"`
	Protected bool   `json:"protected"`
}
type CleanupRequest struct {
	WorkflowID int64
	RevisionID *int64
}
type CleanupHandler func(context.Context, *gorm.DB, CleanupRequest) error
type IngressRequest struct {
	Revision       RevisionRef
	NodeInstanceID string
	Config         json.RawMessage
	Secrets        SecretReader
	Request        *http.Request
	Data           json.RawMessage
	EventTime      time.Time
}
type IngressHandler func(context.Context, IngressRequest) (cloudevents.Event, error)
type RunPanelDescriptor struct {
	PanelKey       string
	Title          string
	NodeTypes      []string
	ComponentEntry string
}

type WorkflowResource struct {
	WorkflowID     int64
	NodeInstanceID string
}

type ResultPageDescriptor struct {
	Resources            func(json.RawMessage) ([]WorkflowResource, error)
	PermissionCode       string
	ActionPermissions    map[string]string
	ConfigComponentEntry string
	ValidateScope        func(context.Context, *gorm.DB, json.RawMessage) error

	PageKey        string
	Title          string
	ComponentEntry string
	ScopeSchema    json.RawMessage
	FilterSchema   json.RawMessage
	Actions        []string
	Mobile         bool
}

type PageDescriptor struct {
	PermissionCode string

	PageKey   string
	Title     string
	Icon      string
	KeepAlive bool
}

type RegisteredPage struct {
	PluginID string
	Menu     PluginMenuDescriptor
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
	RevisionID string
	UserID     int64

	PluginID       string
	WorkflowID     string
	NodeInstanceID string
}

func (WorkflowScope) routeScope() {}

type ResultScope struct {
	Resources      []WorkflowResource
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
	Decide(context.Context, int64, string) error
}

type SystemScope struct {
	WorkflowIDs  []int64
	AllWorkflows bool
	SessionValid func(context.Context) error

	PluginID  string
	UserID    int64
	RoleCodes []string
}

func (SystemScope) routeScope() {}

type AssistantQueryDescriptor struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type AssistantQueryHandler interface {
	Query(context.Context, json.RawMessage, SystemScope) (json.RawMessage, error)
}

type AssistantQueryHandlerFunc func(context.Context, json.RawMessage, SystemScope) (json.RawMessage, error)

func (f AssistantQueryHandlerFunc) Query(ctx context.Context, input json.RawMessage, scope SystemScope) (json.RawMessage, error) {
	return f(ctx, input, scope)
}

type RegisteredAssistantQuery struct {
	PluginID   string
	ToolName   string
	Descriptor AssistantQueryDescriptor
}

type ScopedRouteHandler func(*gin.Context, RouteScope)

type RouteDescriptor struct {
	PermissionCode string

	Method    string
	Pattern   string
	Scope     ScopeKind
	Action    string
	WebSocket bool
}

type RegisteredRoute struct {
	PluginID   string
	Descriptor RouteDescriptor
	Handler    ScopedRouteHandler
}

type PluginStore interface {
	PluginID() string
	DB() *gorm.DB
}

type PluginStoreProvider interface {
	ForPlugin(string) PluginStore
}

type NetworkClient interface {
	Do(*http.Request) (*http.Response, error)
	DoProxied(*http.Request, *url.URL) (*http.Response, error)
	DoPrivate(*http.Request) (*http.Response, error)
	DoPrivateProxied(*http.Request, *url.URL) (*http.Response, error)
	ValidateWebSocketURL(context.Context, *url.URL, bool) error
	ValidateProxiedWebSocketURL(*url.URL, bool) error
	ValidatePrivateWebSocketURL(context.Context, *url.URL) error
	ValidatePrivateProxiedWebSocketURL(*url.URL) error
	DialContext(context.Context, string, string) (net.Conn, error)
	ResolvePublicDomain(context.Context, string) ([]netip.Addr, error)
	SetTimeout(time.Duration)
	DisableRedirects()
}

type NetworkClientFactory interface {
	New([]string) (NetworkClient, error)
}

type OutboundProxyResolver interface {
	ResolveOutboundProxy(context.Context, int64) (string, error)
}

type RealtimePublisher interface {
	PublishInAppNotification(context.Context, int64, int64)
}

type RecipientTarget struct {
	TargetType string `json:"targetType"`
	TargetID   int64  `json:"targetId"`
}
type InboxRequest struct {
	Revision                                                 RevisionRef
	OperationKey, NodeInstanceID, SubjectKey, Title, Message string
	Targets                                                  []RecipientTarget
}
type InboxService interface {
	DeliverInbox(context.Context, InboxRequest) (json.RawMessage, error)
}

func (r RevisionRef) IDs() (int64, int64, error) {
	w, e := strconv.ParseInt(r.WorkflowID, 10, 64)
	v, f := strconv.ParseInt(r.RevisionID, 10, 64)
	if e != nil || f != nil || w <= 0 || v <= 0 {
		return 0, 0, errors.New("invalid revision reference")
	}
	return w, v, nil
}

type Host struct {
	Inbox         InboxService
	Store         PluginStore
	Stores        PluginStoreProvider
	Network       NetworkClientFactory
	OutboundProxy OutboundProxyResolver
	Realtime      RealtimePublisher
	Events        Emitter

	AllowedHTTPHosts []string
}
