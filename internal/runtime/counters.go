package runtimecfg

import "sync"

type ProfileCounters struct {
	mu       sync.Mutex
	profiles map[string]*ProfileSequence
}

type ProfileSequence struct {
	ProfileRequest uint64
	TemplateRender uint64
}

func NewProfileCounters() *ProfileCounters {
	return &ProfileCounters{profiles: make(map[string]*ProfileSequence)}
}

func (c *ProfileCounters) IncrementProfileRequest(profile string) uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seq := c.sequence(profile)
	seq.ProfileRequest++
	return seq.ProfileRequest
}

func (c *ProfileCounters) IncrementTemplateRender(profile string) uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seq := c.sequence(profile)
	seq.TemplateRender++
	return seq.TemplateRender
}

func (c *ProfileCounters) sequence(profile string) *ProfileSequence {
	seq := c.profiles[profile]
	if seq == nil {
		seq = &ProfileSequence{}
		c.profiles[profile] = seq
	}
	return seq
}
