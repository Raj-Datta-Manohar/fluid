package types

import "time"

// ServiceStatus represents endpoint health.
type ServiceStatus string

const (
	StatusUnknown   ServiceStatus = "unknown"
	StatusHealthy   ServiceStatus = "healthy"
	StatusUnhealthy ServiceStatus = "unhealthy"
)

// ServiceEndpoint describes a single instance of a service.
// Note: LastHeartbeatUnixNano is used instead of time.Time for protobuf compatibility.
type ServiceEndpoint struct {
	IP                    string        `json:"ip" protobuf:"bytes,1,opt,name=ip,proto3"`
	Port                  int           `json:"port" protobuf:"varint,2,opt,name=port,proto3"`
	Region                string        `json:"region" protobuf:"bytes,3,opt,name=region,proto3"`
	AZ                    string        `json:"az" protobuf:"bytes,4,opt,name=az,proto3"`
	Status                ServiceStatus `json:"status" protobuf:"bytes,5,opt,name=status,proto3"`
	LastHeartbeatUnixNano int64         `json:"lastHeartbeatUnixNano" protobuf:"varint,6,opt,name=lastHeartbeatUnixNano,proto3"`
}

// NowUnixNano returns current time in unix nano for convenience.
func NowUnixNano() int64 { return time.Now().UnixNano() }

// ServiceInfo groups endpoints by logical service.
type ServiceInfo struct {
	Name      string            `json:"name" protobuf:"bytes,1,opt,name=name,proto3"`
	Version   string            `json:"version" protobuf:"bytes,2,opt,name=version,proto3"`
	Endpoints []ServiceEndpoint `json:"endpoints" protobuf:"bytes,3,rep,name=endpoints,proto3"`
}

// Event type identifiers for registration and routing.
const (
	EventTypeCreateApp          = "CreateAppEvent"
	EventTypeDeleteApp          = "DeleteAppEvent"
	EventTypeUpdateGlobalConfig = "UpdateGlobalConfigEvent"
)

// CreateAppEvent is a critical, strongly consistent operation.
type CreateAppEvent struct {
	AppID string `json:"appId" protobuf:"bytes,1,opt,name=appId,proto3"`
	IP    string `json:"ip" protobuf:"bytes,2,opt,name=ip,proto3"`
}

// DeleteAppEvent removes an app from the catalog.
type DeleteAppEvent struct {
	AppID string `json:"appId" protobuf:"bytes,1,opt,name=appId,proto3"`
}

// UpdateGlobalConfigEvent updates a global setting.
type UpdateGlobalConfigEvent struct {
	Key   string `json:"key" protobuf:"bytes,1,opt,name=key,proto3"`
	Value string `json:"value" protobuf:"bytes,2,opt,name=value,proto3"`
}
