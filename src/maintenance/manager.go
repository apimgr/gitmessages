package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type Config struct {
	RetryInterval string
	MaxAttempts   int
}

type SelfHealingInfo struct {
	Enabled     bool    `json:"enabled"`
	Exhausted   bool    `json:"exhausted"`
	Attempts    int     `json:"attempts"`
	LastAttempt *string `json:"last_attempt"`
	NextAttempt *string `json:"next_attempt"`
}

type Info struct {
	Reason      string          `json:"reason"`
	Message     string          `json:"message"`
	Since       string          `json:"since"`
	SelfHealing SelfHealingInfo `json:"self_healing"`
}

type HealFunc func(context.Context, string) error
type StateFunc func(Info)

type Manager struct {
	mu          sync.RWMutex
	config      Config
	interval    time.Duration
	heal        HealFunc
	onEnter     StateFunc
	onRecover   StateFunc
	onExhausted StateFunc
	info        Info
	active      bool
	started     bool
	ctx         context.Context
	cancel      context.CancelFunc
	wake        chan struct{}
	done        chan struct{}
}

func New(cfg Config, heal HealFunc) (*Manager, error) {
	if heal == nil {
		return nil, errors.New("maintenance self-healing callback is required")
	}
	if cfg.RetryInterval == "" {
		cfg.RetryInterval = "30s"
	}
	interval, err := time.ParseDuration(cfg.RetryInterval)
	if err != nil || interval <= 0 {
		return nil, fmt.Errorf("maintenance.self_healing.retry_interval must be a positive duration")
	}
	if cfg.MaxAttempts < 0 {
		return nil, errors.New("maintenance.self_healing.max_attempts cannot be negative")
	}
	return &Manager{
		config: cfg, interval: interval, heal: heal,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}, nil
}

func (manager *Manager) SetHandlers(onEnter, onRecover, onExhausted StateFunc) {
	manager.mu.Lock()
	manager.onEnter = onEnter
	manager.onRecover = onRecover
	manager.onExhausted = onExhausted
	manager.mu.Unlock()
}

func (manager *Manager) Start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	manager.mu.Lock()
	if manager.started {
		manager.mu.Unlock()
		return
	}
	manager.ctx, manager.cancel = context.WithCancel(parent)
	manager.started = true
	manager.mu.Unlock()
	go manager.run()
	manager.signal()
}

func (manager *Manager) Enter(reason, message string) {
	reason = strings.TrimSpace(reason)
	message = strings.TrimSpace(message)
	if reason == "" {
		reason = "critical_error"
	}
	if message == "" {
		message = "A critical server error requires operator attention"
	}
	manager.mu.Lock()
	if manager.active {
		manager.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	next := formatTime(now.Add(manager.interval))
	manager.info = Info{
		Reason: reason, Message: message, Since: formatTime(now),
		SelfHealing: SelfHealingInfo{Enabled: true, LastAttempt: nil, NextAttempt: &next},
	}
	manager.active = true
	onEnter := manager.onEnter
	info := manager.info
	manager.mu.Unlock()
	log.Printf("WARN maintenance mode entered reason=%s message=%s", reason, message)
	if onEnter != nil {
		onEnter(info)
	}
	manager.signal()
}

func (manager *Manager) Snapshot() (Info, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.info, manager.active
}

func (manager *Manager) Stop() {
	manager.mu.Lock()
	if manager.cancel != nil {
		manager.cancel()
	}
	started := manager.started
	manager.mu.Unlock()
	if started {
		<-manager.done
	}
}

func (manager *Manager) signal() {
	select {
	case manager.wake <- struct{}{}:
	default:
	}
}

func (manager *Manager) run() {
	defer close(manager.done)
	for {
		manager.mu.RLock()
		active := manager.active
		exhausted := manager.info.SelfHealing.Exhausted
		nextText := manager.info.SelfHealing.NextAttempt
		ctx := manager.ctx
		manager.mu.RUnlock()
		if ctx == nil {
			return
		}
		if !active || exhausted || nextText == nil {
			select {
			case <-ctx.Done():
				return
			case <-manager.wake:
				continue
			}
		}
		next, err := time.Parse(time.RFC3339, *nextText)
		if err != nil {
			manager.scheduleNext()
			continue
		}
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-manager.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			manager.attempt(ctx)
		}
	}
}

func (manager *Manager) attempt(ctx context.Context) {
	manager.mu.Lock()
	if !manager.active || manager.info.SelfHealing.Exhausted {
		manager.mu.Unlock()
		return
	}
	manager.info.SelfHealing.Attempts++
	attempts := manager.info.SelfHealing.Attempts
	reason := manager.info.Reason
	last := formatTime(time.Now().UTC())
	manager.info.SelfHealing.LastAttempt = &last
	manager.info.SelfHealing.NextAttempt = nil
	info := manager.info
	manager.mu.Unlock()
	log.Printf("INFO maintenance self-healing attempt=%d reason=%s", attempts, reason)
	if err := manager.heal(ctx, reason); err == nil {
		manager.recover(info)
		return
	}
	manager.mu.Lock()
	if manager.config.MaxAttempts > 0 && manager.info.SelfHealing.Attempts >= manager.config.MaxAttempts {
		manager.info.SelfHealing.Enabled = false
		manager.info.SelfHealing.Exhausted = true
		manager.info.SelfHealing.NextAttempt = nil
		info = manager.info
		onExhausted := manager.onExhausted
		manager.mu.Unlock()
		log.Printf("WARN maintenance self-healing exhausted reason=%s attempts=%d", reason, attempts)
		if onExhausted != nil {
			onExhausted(info)
		}
		return
	}
	next := formatTime(time.Now().UTC().Add(manager.interval))
	manager.info.SelfHealing.Enabled = true
	manager.info.SelfHealing.NextAttempt = &next
	manager.mu.Unlock()
}

func (manager *Manager) recover(previous Info) {
	manager.mu.Lock()
	if !manager.active {
		manager.mu.Unlock()
		return
	}
	manager.active = false
	manager.info = Info{}
	onRecover := manager.onRecover
	manager.mu.Unlock()
	log.Printf("INFO Issue resolved, exiting maintenance mode reason=%s", previous.Reason)
	if onRecover != nil {
		onRecover(previous)
	}
}

func (manager *Manager) scheduleNext() {
	manager.mu.Lock()
	if manager.active && !manager.info.SelfHealing.Exhausted {
		next := formatTime(time.Now().UTC().Add(manager.interval))
		manager.info.SelfHealing.NextAttempt = &next
	}
	manager.mu.Unlock()
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339) }
