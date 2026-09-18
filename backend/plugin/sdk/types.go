// Package sdk defines the public compile-time plugin contract.
package sdk

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type NodeKind string
type ExecutionPool string
type SideEffectClass string
type StateMode string
type NodeRole string
type NodeVisibility string

// NodeRole controls how a node is presented and composed in the workflow
// editor.  Kind still determines the executable contract; Role only carries
// the editor/runtime intent of the node.
const (
	NodeRoleTrigger NodeRole = "trigger"
	// NodeRoleEntry is the editor-oriented spelling for an entry node. New
	// descriptors should prefer NodeRoleTrigger when the node emits events.
	NodeRoleEntry   NodeRole = "entry"
	NodeRoleData    NodeRole = "data"
	NodeRoleCompute NodeRole = "compute"
	NodeRoleControl NodeRole = "control"
	NodeRoleEffect  NodeRole = "effect"
	NodeRoleEnd     NodeRole = "end"
)

const (
	NodeVisibilityBasic    NodeVisibility = "basic"
	NodeVisibilityAdvanced NodeVisibility = "advanced"
)

// ConfigGroup lets a plugin arrange a node's configuration into small,
// discoverable sections without putting presentation rules into core.
type ConfigGroup struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Fields      []string `json:"fields,omitempty"`
	Advanced    bool     `json:"advanced"`
}

// ProfileDescriptor describes a profile type owned by a plugin.  Core stores
// references to profile instances and versions; the plugin owns the profile's
// schema, UI and runtime resolution.
type ProfileDescriptor struct {
	Type         string          `json:"type"`
	Version      string          `json:"version"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	ConfigSchema json.RawMessage `json:"configSchema"`
	UISchema     json.RawMessage `json:"uiSchema"`
}

// ProfileRef is the immutable reference embedded in a workflow revision and
// run snapshot.  Version is required so publishing a new profile never
// changes an existing revision implicitly.
type ProfileRef struct {
	PluginID  string `json:"pluginId"`
	ProfileID string `json:"profileId"`
	Version   string `json:"version"`
	Type      string `json:"type"`
}

// ProfileSlot declares the profile capability a node needs.  The editor can
// offer only compatible profile types while the runtime persists the stable
// profile id and pinned version.
type ProfileSlot struct {
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	ProfileTypes []string `json:"profileTypes"`
	Required     bool     `json:"required"`
}

// ResolvedProfile is the runtime value returned by the owning plugin. Core
// never interprets Config; it only pins Ref into a revision/run snapshot.
type ResolvedProfile struct {
	Ref     ProfileRef      `json:"ref"`
	Name    string          `json:"name"`
	Summary string          `json:"summary"`
	Config  json.RawMessage `json:"config"`
	Secrets SecretReader    `json:"-"`
}

type ProfileResolver interface {
	Resolve(context.Context, ProfileRef) (ResolvedProfile, error)
}

type ProfileStatus string

const (
	ProfileStatusDraft     ProfileStatus = "draft"
	ProfileStatusActive    ProfileStatus = "active"
	ProfileStatusDisabled  ProfileStatus = "disabled"
	ProfileStatusPublished ProfileStatus = "published"
)

type ProfileVersionSummary struct {
	Version     string        `json:"version"`
	Status      ProfileStatus `json:"status"`
	PublishedAt *time.Time    `json:"publishedAt,omitempty"`
}

type ProfileSummary struct {
	PluginID               string                  `json:"pluginId"`
	ID                     string                  `json:"id"`
	Name                   string                  `json:"name"`
	Summary                string                  `json:"summary"`
	Type                   string                  `json:"type"`
	Status                 ProfileStatus           `json:"status"`
	LatestPublishedVersion string                  `json:"latestPublishedVersion,omitempty"`
	Versions               []ProfileVersionSummary `json:"versions,omitempty"`
	Config                 json.RawMessage         `json:"config,omitempty"`
	ConfigSchema           json.RawMessage         `json:"configSchema"`
	UISchema               json.RawMessage         `json:"uiSchema"`
}

type ProfileListQuery struct {
	PluginID        string
	Type            string
	IncludeDisabled bool
	Limit           int
}

type ProfileActor struct {
	UserID    int64
	RoleCodes []string
}

type ProfilePermission string

const (
	ProfilePermissionRead    ProfilePermission = "profile.read"
	ProfilePermissionWrite   ProfilePermission = "profile.write"
	ProfilePermissionPublish ProfilePermission = "profile.publish"
	ProfilePermissionDisable ProfilePermission = "profile.disable"
)

// ProfileProvider is implemented by the plugin that owns a profile type.
// Core can list and pin references through this interface but never owns the
// profile configuration or secrets.
type ProfileProvider interface {
	List(context.Context, ProfileListQuery) ([]ProfileSummary, error)
	ValidateNewReference(context.Context, ProfileRef) error
	Resolve(context.Context, ProfileRef) (ResolvedProfile, error)
}

type NodeCapabilities struct {
	Deterministic bool `json:"deterministic"`
	Stateless     bool `json:"stateless"`
	FrameSafe     bool `json:"frameSafe"`
	FrameDriver   bool `json:"frameDriver"`
	FrameResult   bool `json:"frameResult"`
	ManualTrigger bool `json:"manualTrigger"`
}

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
	Title        string
	Description  string
	Category     string
	Aliases      []string
	Tags         []string
	SortOrder    int
	Color        string
	Icon         string
	Width        int
	Height       int
	Capabilities NodeCapabilities
	Branches     []string
	// FrameSourcePorts limits the output ports that a frame-driven operation
	// may use to enter its deterministic per-item subgraph. An empty value
	// keeps the legacy descriptor behavior of allowing the operation to choose
	// its port at runtime.
	FrameSourcePorts []string
	ConfigSchema     json.RawMessage
	UISchema         json.RawMessage
	InputSchema      json.RawMessage
	OutputSchema     json.RawMessage
	Pool             ExecutionPool
	SideEffect       SideEffectClass
	State            StateMode
	ValidateConfig   func(json.RawMessage) error
	ConnectionType   string
	ConnectionFields []string
	// EventSubscription 声明本地事件入口，不启动重复的外部连接。
	EventSubscription func(json.RawMessage) (EventSubscription, error)
	TriggerOutput     func(json.RawMessage, json.RawMessage, time.Time) (json.RawMessage, error)
	OperationConfig   func(json.RawMessage, []json.RawMessage) (json.RawMessage, error)
	WorkflowOperation bool
	Role              NodeRole
	Visibility        NodeVisibility
	ProfileSlots      []ProfileSlot
	ConfigGroups      []ConfigGroup
}

type EventSubscription struct {
	Types   []string `json:"types"`
	Source  string   `json:"source,omitempty"`
	Subject string   `json:"subject,omitempty"`
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
	Revision           RevisionRef
	NodeInstanceID     string
	ProfileBindings    map[string]ProfileRef
	OperationKey       string
	Input              json.RawMessage
	Config             json.RawMessage
	Secrets            SecretReader
	State              StateStore
	Artifacts          ArtifactStore
	Frames             FrameExecutor
	Incoming           []NodeOutput
	FrameContext       json.RawMessage
	FrameResultNodeIDs []string
	ExecutionMode      string
	Logger             *slog.Logger
	TriggeredAt        time.Time
	Resume             *ResumeContext
	Profiles           ProfileResolver
}

type ResumeContext struct {
	Data     json.RawMessage
	TimedOut bool
}

// 等待状态由核心持久化；插件只声明唤醒条件和需要保留的领域上下文。
type WaitRequest struct {
	BlockFollowingRuns bool
	Key                string
	Until              time.Time
	WakeAt             time.Time
	Subscription       EventSubscription
	Data               json.RawMessage
}

const (
	ExecutionModeWorkflow      = "workflow"
	ExecutionModeBacktestFrame = "backtest_frame"
)

type FrameRequest struct {
	SourcePort    string
	SourceOutput  json.RawMessage
	Event         map[string]string
	Context       json.RawMessage
	ResultNodeIDs []string
}

type FrameResult struct {
	NodeOutputs map[string]json.RawMessage
	Results     []json.RawMessage
}

type FrameExecutor interface {
	ExecuteFrame(context.Context, FrameRequest) (FrameResult, error)
}

type NodeOutput struct {
	NodeInstanceID string
	Output         json.RawMessage
}

type ActionResult struct {
	Output    json.RawMessage
	Artifacts []Artifact
	Port      string
	Skip      bool
	Wait      *WaitRequest
}

type ActionHandler interface {
	Execute(context.Context, ActionRequest) (ActionResult, error)
}

type TriggerRequest struct {
	Revision        RevisionRef
	NodeInstanceID  string
	ProfileBindings map[string]ProfileRef
	Profiles        ProfileResolver
	Config          json.RawMessage
	Secrets         SecretReader
	State           StateStore
	Logger          *slog.Logger
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

type Candle struct {
	OpenTime  time.Time
	CloseTime time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
}

type Instrument struct {
	Market       string
	Symbol       string
	BaseAsset    string
	QuoteAsset   string
	Status       string
	PriceTick    decimal.Decimal
	QuantityStep decimal.Decimal
	MinQuantity  decimal.Decimal
	UpdatedAt    time.Time
}

type InstrumentQuery struct {
	Markets     []string
	Instruments []string
	Limit       int
	ProxyID     int64
}

type CandleQuery struct {
	ClosedThrough time.Time
	Market        string
	Instrument    string
	Interval      string
	StartTime     time.Time
	EndTime       time.Time
	Limit         int
	ProxyID       int64
}

type QuoteQuery struct {
	Market     string
	Instrument string
	ProxyID    int64
}

type Quote struct {
	Price    decimal.Decimal
	QuotedAt time.Time
}

type MarketSeries struct {
	Market        string
	Instrument    string
	Interval      string
	FirstOpenTime time.Time
	LastCloseTime time.Time
	CandleCount   int64
	WorkflowIDs   []int64
}

type MarketDataProvider interface {
	Series(context.Context) ([]MarketSeries, error)
	CandleSubscription(CandleQuery) EventSubscription
	ID() string
	Instruments(context.Context, InstrumentQuery) ([]Instrument, error)
	Candles(context.Context, CandleQuery) ([]Candle, error)
	Quote(context.Context, QuoteQuery) (Quote, error)
}

type MarketDataRegistry interface {
	MarketDataProvider(string) (MarketDataProvider, bool)
}

type OrderRequest struct {
	Account        string
	Market         string
	Instrument     string
	Side           string
	Quantity       decimal.Decimal
	QuoteAmount    decimal.Decimal
	PositionEffect string
	ClientOrderID  string
	Secrets        SecretReader
	ProxyID        int64
}

type OrderQuery struct {
	Account       string
	Market        string
	Instrument    string
	OrderID       string
	ClientOrderID string
	Secrets       SecretReader
	ProxyID       int64
}

type CancelOrderRequest = OrderQuery

type OrderResult struct {
	ProviderOrderID string
	ClientOrderID   string
	Status          string
	Market          string
	Instrument      string
	Side            string
	Quantity        decimal.Decimal
	Executed        decimal.Decimal
	AveragePrice    decimal.Decimal
	UpdatedAt       time.Time
}

type ExecutionProvider interface {
	ID() string
	PlaceOrder(context.Context, OrderRequest) (OrderResult, error)
	GetOrder(context.Context, OrderQuery) (OrderResult, error)
	CancelOrder(context.Context, CancelOrderRequest) error
}

type ExecutionRegistry interface {
	ExecutionProvider(string) (ExecutionProvider, bool)
}

type StrategyRegistry interface {
	Strategy(string) (StrategyDescriptor, Strategy, bool)
	Strategies() []StrategyDescriptor
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

type Host struct {
	Store            PluginStore
	Stores           PluginStoreProvider
	Network          NetworkClientFactory
	OutboundProxy    OutboundProxyResolver
	Realtime         RealtimePublisher
	Events           Emitter
	MarketData       MarketDataRegistry
	Execution        ExecutionRegistry
	Strategies       StrategyRegistry
	Profiles         ProfileResolver
	AllowedHTTPHosts []string
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
