package consensus

import "fmt"

// NotLeaderError indicates the node is not the leader and, if known, where the leader is.
type NotLeaderError struct {
	LeaderAddr string
}

func (e *NotLeaderError) Error() string {
	if e.LeaderAddr == "" {
		return "node is not the leader"
	}
	return fmt.Sprintf("node is not the leader; leader=%s", e.LeaderAddr)
}

// IsNotLeader returns the embedded *NotLeaderError and a bool indicating match.
func IsNotLeader(err error) (*NotLeaderError, bool) {
	if err == nil {
		return nil, false
	}
	ne, ok := err.(*NotLeaderError)
	return ne, ok
}
