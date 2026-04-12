// Package manager orchestrates binary discovery, dependency ordering,
// lifecycle management, and file watching.
package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/slidebolt/sb-manager/internal/discovery"
	"github.com/slidebolt/sb-manager/internal/process"
)

const (
	readyTimeout    = 30 * time.Second
	shutdownTimeout = 10 * time.Second
	watchInterval   = 2 * time.Second
)

// Manager orchestrates binary lifecycle.
type Manager struct {
	binDir      string
	overrideDir string

	mu        sync.Mutex
	processes map[string]*process.Process // keyed by binary ID
	binaries  map[string]discovery.Binary // keyed by binary ID
	modTimes  map[string]time.Time        // keyed by file path

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a Manager watching the given bin directory and optional override directory.
func New(binDir, overrideDir string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		binDir:      binDir,
		overrideDir: overrideDir,
		processes:   make(map[string]*process.Process),
		binaries:    make(map[string]discovery.Binary),
		modTimes:    make(map[string]time.Time),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Run performs initial discovery and starts all binaries, then watches
// for file changes. Blocks until ctx is cancelled or Shutdown is called.
func (m *Manager) Run() error {
	runStarted := time.Now()
	if err := os.MkdirAll(m.binDir, 0755); err != nil {
		return fmt.Errorf("manager: ensure bin dir: %w", err)
	}

	if m.overrideDir != "" {
		if err := os.MkdirAll(m.overrideDir, 0755); err != nil {
			slog.Warn("manager: failed to ensure override dir", "error", err)
		}
	}

	discoverStarted := time.Now()
	if err := m.discover(); err != nil {
		return err
	}
	slog.Info("manager: discovery completed", "duration", time.Since(discoverStarted).Round(time.Millisecond), "binaries", len(m.binaries))

	startStarted := time.Now()
	if err := m.startAll(); err != nil {
		return err
	}
	slog.Info("manager: startup completed", "duration", time.Since(startStarted).Round(time.Millisecond), "total_startup", time.Since(runStarted).Round(time.Millisecond), "processes", len(m.processes))

	m.watchLoop()
	return nil
}

// Shutdown stops all managed processes gracefully.
func (m *Manager) Shutdown() {
	slog.Info("shutting down all processes")

	m.mu.Lock()
	procs := make([]*process.Process, 0, len(m.processes))
	// Shutdown in reverse dependency order (simple: just shut them all down).
	for _, p := range m.processes {
		procs = append(procs, p)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go func(p *process.Process) {
			defer wg.Done()
			if err := p.Shutdown(shutdownTimeout); err != nil {
				slog.Warn("shutdown error", "id", p.ID, "error", err)
			} else {
				slog.Info("stopped", "id", p.ID)
			}
		}(p)
	}
	wg.Wait()

	// Cancel the context after graceful shutdown so exec.CommandContext
	// does not SIGKILL children before they have a chance to flush.
	m.cancel()
}

func (m *Manager) discover() error {
	started := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.overrideDir != "" {
		overrideBins, errs := discovery.Scan(m.ctx, m.overrideDir)
		for _, err := range errs {
			slog.Warn("override discovery warning", "error", err)
		}
		for _, bin := range overrideBins {
			m.binaries[bin.Manifest.ID] = bin
			if info, err := os.Stat(bin.Path); err == nil {
				m.modTimes[bin.Path] = info.ModTime()
			}
			slog.Info("discovered OVERRIDE", "id", bin.Manifest.ID, "kind", bin.Manifest.Kind, "path", filepath.Base(bin.Path))
		}
	}

	canonicalBins, errs := discovery.Scan(m.ctx, m.binDir)
	for _, err := range errs {
		slog.Warn("discovery warning", "error", err)
	}

	for _, bin := range canonicalBins {
		if _, exists := m.binaries[bin.Manifest.ID]; exists {
			slog.Debug("ignoring canonical binary; override exists", "id", bin.Manifest.ID)
			continue
		}

		m.binaries[bin.Manifest.ID] = bin
		if info, err := os.Stat(bin.Path); err == nil {
			m.modTimes[bin.Path] = info.ModTime()
		}
		slog.Info("discovered", "id", bin.Manifest.ID, "kind", bin.Manifest.Kind, "path", filepath.Base(bin.Path))
	}

	if len(m.binaries) == 0 {
		slog.Warn("no binaries found", "dir", m.binDir)
	}
	slog.Info("manager: discover pass finished", "duration", time.Since(started).Round(time.Millisecond), "binaries", len(m.binaries))

	return nil
}

// topoSort returns binary IDs in dependency order.
func (m *Manager) topoSort() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Build adjacency.
	inDegree := make(map[string]int)
	dependents := make(map[string][]string) // dep → list of things that depend on it

	for id := range m.binaries {
		inDegree[id] = 0
	}

	for id, bin := range m.binaries {
		for _, dep := range bin.Manifest.DependsOn {
			if _, exists := m.binaries[dep]; !exists {
				return nil, fmt.Errorf("toposort: %s depends on %s, which was not discovered", id, dep)
			}
			dependents[dep] = append(dependents[dep], id)
			inDegree[id]++
		}
	}

	// Kahn's algorithm.
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var order []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)

		for _, dep := range dependents[id] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(order) != len(m.binaries) {
		return nil, fmt.Errorf("toposort: cyclic dependency detected")
	}

	return order, nil
}

func (m *Manager) startAll() error {
	started := time.Now()
	order, err := m.topoSort()
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}

	for _, id := range order {
		if err := m.startBinary(id); err != nil {
			return fmt.Errorf("manager: start %s: %w", id, err)
		}
	}
	slog.Info("manager: startAll finished", "duration", time.Since(started).Round(time.Millisecond), "count", len(order))

	return nil
}

func (m *Manager) startBinary(id string) error {
	started := time.Now()
	m.mu.Lock()
	bin, ok := m.binaries[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown binary %s", id)
	}

	p := process.New(id, bin.Path)
	slog.Info("starting", "id", id)

	if err := p.Start(m.ctx); err != nil {
		return err
	}

	m.mu.Lock()
	m.processes[id] = p
	m.mu.Unlock()

	// Send dependency payloads before the binary can become ready.
	for _, depID := range bin.Manifest.DependsOn {
		m.mu.Lock()
		depProc, exists := m.processes[depID]
		m.mu.Unlock()

		var payload json.RawMessage
		if exists {
			payload = depProc.Payload
		}

		if err := p.SendDependency(depID, payload); err != nil {
			return fmt.Errorf("send dependency %s to %s: %w", depID, id, err)
		}
		slog.Debug("sent dependency", "dep", depID, "to", id)
	}

	// Wait for ready or timeout.
	select {
	case <-p.Ready:
		slog.Info("ready", "id", id, "startup", time.Since(started).Round(time.Millisecond))
	case <-p.Done:
		return fmt.Errorf("%s exited before becoming ready: %v", id, p.Err)
	case <-time.After(readyTimeout):
		return fmt.Errorf("%s did not become ready within %s", id, readyTimeout)
	}

	// Monitor for crashes.
	go m.monitorCrash(id, p)

	return nil
}

func (m *Manager) monitorCrash(id string, p *process.Process) {
	<-p.Done
	if m.ctx.Err() != nil {
		return // Manager is shutting down, don't restart.
	}
	if p.State() == process.StateFailed {
		slog.Warn("crashed, restarting", "id", id)
		time.Sleep(1 * time.Second)
		if err := m.startBinary(id); err != nil {
			slog.Error("restart failed", "id", id, "error", err)
		}
	}
}

func (m *Manager) watchLoop() {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.reconcile()
		}
	}
}

func (m *Manager) reconcile() {
	started := time.Now()
	// 1. Discover all current binaries
	currentBinaries := make(map[string]discovery.Binary)
	currentModTimes := make(map[string]time.Time)

	if m.overrideDir != "" {
		overrideBins, _ := discovery.Scan(m.ctx, m.overrideDir)
		for _, bin := range overrideBins {
			currentBinaries[bin.Manifest.ID] = bin
			if info, err := os.Stat(bin.Path); err == nil {
				currentModTimes[bin.Path] = info.ModTime()
			}
		}
	}

	canonicalBins, _ := discovery.Scan(m.ctx, m.binDir)
	for _, bin := range canonicalBins {
		if _, exists := currentBinaries[bin.Manifest.ID]; !exists {
			currentBinaries[bin.Manifest.ID] = bin
			if info, err := os.Stat(bin.Path); err == nil {
				currentModTimes[bin.Path] = info.ModTime()
			}
		}
	}

	// 2. Diff with current state
	m.mu.Lock()
	type action struct {
		id      string
		bin     discovery.Binary
		restart bool
		stop    bool
	}
	var actions []action

	for id, newBin := range currentBinaries {
		oldBin, exists := m.binaries[id]
		if !exists {
			actions = append(actions, action{id: id, bin: newBin, restart: true})
		} else {
			oldMod := m.modTimes[oldBin.Path]
			newMod := currentModTimes[newBin.Path]
			if oldBin.Path != newBin.Path || newMod.After(oldMod) {
				actions = append(actions, action{id: id, bin: newBin, restart: true})
			}
		}
	}

	for id := range m.binaries {
		if _, exists := currentBinaries[id]; !exists {
			actions = append(actions, action{id: id, stop: true})
		}
	}
	m.mu.Unlock()

	// 3. Apply actions
	for _, act := range actions {
		m.mu.Lock()
		existingProc, running := m.processes[act.id]
		m.mu.Unlock()

		if running {
			slog.Info("stopping for restart/removal", "id", act.id)
			existingProc.Shutdown(shutdownTimeout)
			m.mu.Lock()
			delete(m.processes, act.id)
			m.mu.Unlock()
		}

		if act.stop {
			m.mu.Lock()
			delete(m.binaries, act.id)
			// ModTimes cleanup happens below
			m.mu.Unlock()
			continue
		}

		if act.restart {
			m.mu.Lock()
			m.binaries[act.id] = act.bin
			m.modTimes[act.bin.Path] = currentModTimes[act.bin.Path]
			m.mu.Unlock()

			if err := m.startBinary(act.id); err != nil {
				slog.Error("failed to start updated binary", "id", act.id, "error", err)
			}
		}
		if len(actions) > 0 {
			slog.Info("manager: reconcile applied changes", "actions", len(actions), "duration", time.Since(started).Round(time.Millisecond))
		}
	}

	// Cleanup old modTimes
	m.mu.Lock()
	for path := range m.modTimes {
		found := false
		for _, b := range m.binaries {
			if b.Path == path {
				found = true
				break
			}
		}
		if !found {
			delete(m.modTimes, path)
		}
	}
	m.mu.Unlock()
}
