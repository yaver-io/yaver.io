package main

// tmux_input_queue.go — serialize keystroke delivery per tmux target.
//
// The bug this exists to make impossible:
//
// sendTmuxLine is not one operation. It is three — `send-keys -l <text>`, a
// 250ms beat (tmuxSubmitDelay, which exists because coding-agent composers
// swallow an Enter that arrives too soon), then `send-keys Enter`. Nothing
// held the pane across those three steps. Two senders hitting the same pane
// concurrently interleave inside that window, and the failure is not a dropped
// message — it is a CORRUPTED one:
//
//	sender A: send-keys -l "deploy the staging build"
//	sender B: send-keys -l "no wait, revert"
//	sender A: send-keys Enter        ← submits "deploy the staging buildno wait, revert"
//	sender B: send-keys Enter        ← submits an empty line into the composer
//
// One prompt was silently fused out of two people's words and handed to an
// agent with shell access, and both senders were told "sent". There is no error
// anywhere in that sequence.
//
// This is not hypothetical concurrency: it is the normal case the product is
// moving toward. A tmux session can be driven at the same time by the phone,
// the desktop, a voice turn, an autorun loop, and — once sessions are shared —
// by a second person. Every one of those paths lands in SendTmuxInput.
//
// The fix is a per-target FIFO queue. Each submission is enqueued as ONE unit
// and executed to completion before the next unit for that target starts, so
// text and its Enter can never be split by someone else's text. Queues are
// per-target, so two different panes still run in parallel — a global lock
// would make one slow pane stall every other session on the box.
//
// Ordering is FIFO on purpose. sync.Mutex would give atomicity but Go makes no
// fairness guarantee, so two messages typed in order could still be delivered
// out of order — which in a conversation reads as the agent losing its mind.

import (
	"fmt"
	"sync"
)

// tmuxInputQueueDepth bounds how many submissions may wait per target.
//
// Bounded, and it fails LOUDLY rather than dropping. A silent drop here is the
// same class of bug as the interleave: the sender is told nothing and the words
// never arrive. If this depth is ever hit, something is wrong upstream (a
// runaway loop, a stuck pane) and the caller needs to know.
const tmuxInputQueueDepth = 64

// tmuxInputUnit is one indivisible delivery: everything between "start typing"
// and "the composer has been submitted".
type tmuxInputUnit struct {
	run  func() error
	done chan error
}

// tmuxInputQueues owns one serialized worker per tmux target.
type tmuxInputQueues struct {
	mu     sync.Mutex
	queues map[string]chan tmuxInputUnit
}

var tmuxInputQ = &tmuxInputQueues{queues: map[string]chan tmuxInputUnit{}}

// queueFor returns the channel for a target, starting its worker on first use.
//
// Workers are never torn down. A pane's queue is a few dozen bytes and an idle
// goroutine parked on a channel receive; reaping them would introduce a
// race between "worker exiting" and "unit being enqueued" whose only possible
// symptom is a lost keystroke — the exact failure this file removes. Bounded in
// practice by how many distinct panes one box has.
func (q *tmuxInputQueues) queueFor(target string) chan tmuxInputUnit {
	q.mu.Lock()
	defer q.mu.Unlock()
	if ch, ok := q.queues[target]; ok {
		return ch
	}
	ch := make(chan tmuxInputUnit, tmuxInputQueueDepth)
	q.queues[target] = ch
	go func() {
		for unit := range ch {
			unit.done <- unit.run()
		}
	}()
	return ch
}

// submitTmuxInput runs fn as an atomic unit for target, waiting its turn behind
// any submission already queued for that same target. Returns fn's error, or a
// queue-full error — never silently drops.
//
// Callers block until their unit has actually been delivered. That is
// deliberate: the return value of SendTmuxInput is what tells a phone "your
// message went in", and it must not become true before the Enter landed.
func submitTmuxInput(target string, fn func() error) error {
	if target == "" {
		// No target means no serialization domain. Refuse rather than run
		// unserialized — an unkeyed write is precisely the corruption case.
		return fmt.Errorf("tmux input needs a target pane or session to serialize on")
	}
	unit := tmuxInputUnit{run: fn, done: make(chan error, 1)}
	ch := tmuxInputQ.queueFor(target)
	select {
	case ch <- unit:
	default:
		return fmt.Errorf(
			"tmux input queue for %q is full (%d waiting) — the pane is not draining; "+
				"check whether the agent in it is hung before sending more",
			target, tmuxInputQueueDepth)
	}
	return <-unit.done
}
