package service

import "sync"

// RuntimeMetrics is a process-local view of requests that are currently being
// relayed. Ledger aggregation is authoritative for completed calls; this tiny
// tracker fills the gap while a stream is still in flight and never persists
// user prompts, credentials, or response data.
type RuntimeMetrics struct {
	mu              sync.Mutex
	all             int
	waiting         int
	platform        map[string]int
	waitingPlatform map[string]int
	group           map[int64]int
	waitingGroup    map[int64]int
	account         map[int64]int
	user            map[int64]int
}

type RuntimeSnapshot struct {
	InFlight int
	Waiting  int
	Platform map[string]int
	Group    map[int64]int
	Account  map[int64]int
	User     map[int64]int
}

type RuntimeRequest struct {
	metrics   *RuntimeMetrics
	platform  string
	groupID   int64
	userID    int64
	accountID int64
	waiting   bool
	finished  bool
}

func NewRuntimeMetrics() *RuntimeMetrics {
	return &RuntimeMetrics{
		platform: map[string]int{}, waitingPlatform: map[string]int{}, group: map[int64]int{}, waitingGroup: map[int64]int{}, account: map[int64]int{}, user: map[int64]int{},
	}
}

func (m *RuntimeMetrics) Begin(platform string, groupID, userID int64) *RuntimeRequest {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.all++
	m.platform[platform]++
	m.group[groupID]++
	m.user[userID]++
	m.mu.Unlock()
	return &RuntimeRequest{metrics: m, platform: platform, groupID: groupID, userID: userID}
}

func (r *RuntimeRequest) SetAccount(accountID int64) {
	if r == nil || r.metrics == nil || accountID <= 0 || r.finished || r.accountID == accountID {
		return
	}
	m := r.metrics
	m.mu.Lock()
	if r.accountID > 0 {
		decrement(m.account, r.accountID)
	}
	r.accountID = accountID
	m.account[accountID]++
	m.mu.Unlock()
}

// SetGroup moves an in-flight request between selected groups while the
// gateway performs cross-group failover. Platform and user totals stay the
// same; group and waiting-group counters follow the pool currently being
// scheduled.
func (r *RuntimeRequest) SetGroup(groupID int64) {
	if r == nil || r.metrics == nil || groupID <= 0 || r.finished || r.groupID == groupID {
		return
	}
	m := r.metrics
	m.mu.Lock()
	if r.finished || r.groupID == groupID {
		m.mu.Unlock()
		return
	}
	decrement(m.group, r.groupID)
	m.group[groupID]++
	if r.waiting {
		decrement(m.waitingGroup, r.groupID)
		m.waitingGroup[groupID]++
	}
	r.groupID = groupID
	m.mu.Unlock()
}

// SetWaiting marks the request while it is blocked on a user/key or upstream
// account slot. It is idempotent so callers can bracket every bounded wait
// without double-counting transitions between the two limiter layers.
func (r *RuntimeRequest) SetWaiting(waiting bool) {
	if r == nil || r.metrics == nil || r.finished || r.waiting == waiting {
		return
	}
	m := r.metrics
	m.mu.Lock()
	if r.finished || r.waiting == waiting {
		m.mu.Unlock()
		return
	}
	r.waiting = waiting
	if waiting {
		m.waiting++
		m.waitingPlatform[r.platform]++
		m.waitingGroup[r.groupID]++
	} else {
		if m.waiting > 0 {
			m.waiting--
		}
		decrement(m.waitingPlatform, r.platform)
		decrement(m.waitingGroup, r.groupID)
	}
	m.mu.Unlock()
}

func (r *RuntimeRequest) Finish() {
	if r == nil || r.metrics == nil || r.finished {
		return
	}
	m := r.metrics
	m.mu.Lock()
	if r.finished {
		m.mu.Unlock()
		return
	}
	r.finished = true
	if r.waiting {
		r.waiting = false
		if m.waiting > 0 {
			m.waiting--
		}
		decrement(m.waitingPlatform, r.platform)
		decrement(m.waitingGroup, r.groupID)
	}
	if m.all > 0 {
		m.all--
	}
	decrement(m.platform, r.platform)
	decrement(m.group, r.groupID)
	decrement(m.user, r.userID)
	if r.accountID > 0 {
		decrement(m.account, r.accountID)
	}
	m.mu.Unlock()
}

func decrement[K comparable](values map[K]int, key K) {
	if values[key] <= 1 {
		delete(values, key)
		return
	}
	values[key]--
}

func copyCounts[K comparable](source map[K]int) map[K]int {
	result := make(map[K]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (m *RuntimeMetrics) Snapshot(platform string, groupID int64) RuntimeSnapshot {
	if m == nil {
		return RuntimeSnapshot{Platform: map[string]int{}, Group: map[int64]int{}, Account: map[int64]int{}, User: map[int64]int{}}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if platform == "" && groupID == 0 {
		return RuntimeSnapshot{InFlight: m.all, Waiting: m.waiting, Platform: copyCounts(m.platform), Group: copyCounts(m.group), Account: copyCounts(m.account), User: copyCounts(m.user)}
	}
	// A filtered page must not claim it knows account-level values which cannot
	// be safely reconstructed from independent maps. Group is exact; the
	// platform count is exact only when no group filter is selected.
	filtered := RuntimeSnapshot{Platform: map[string]int{}, Group: map[int64]int{}, Account: map[int64]int{}, User: map[int64]int{}}
	if groupID > 0 {
		filtered.InFlight = m.group[groupID]
		filtered.Waiting = m.waitingGroup[groupID]
		if filtered.InFlight > 0 {
			filtered.Group[groupID] = filtered.InFlight
		}
		return filtered
	}
	filtered.InFlight = m.platform[platform]
	filtered.Waiting = m.waitingPlatform[platform]
	if filtered.InFlight > 0 {
		filtered.Platform[platform] = filtered.InFlight
	}
	return filtered
}
