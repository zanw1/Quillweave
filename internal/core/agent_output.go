package core

import (
	"strings"
	"time"
)

// agentTextStream publishes one request's prose while keeping protocol envelopes out of the transcript.
type agentTextStream struct {
	engine     *Engine
	jobID      string
	base       string
	continuous bool
	writing    bool
	prefix     string
	finished   bool
	raw        strings.Builder
}

// newAgentTextStream anchors updates to the transcript that existed before this model request.
func (engine *Engine) newAgentTextStream(jobID string) *agentTextStream {
	stream := &agentTextStream{engine: engine, jobID: jobID}
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	if job := engine.jobs[jobID]; job != nil {
		stream.base = job.Text
	}
	return stream
}

// append publishes the unambiguous prose prefix immediately and buffers possible JSON or code envelopes.
func (stream *agentTextStream) append(text string) {
	stream.raw.WriteString(text)
	stream.publish(agentProsePrefix(stream.raw.String()))
}

// agentProsePrefix buffers possible protocol JSON while allowing ordinary Markdown and inline code to stream.
func agentProsePrefix(text string) string {
	end := len(text)
	if marker := strings.IndexByte(text, '{'); marker >= 0 {
		end = marker
	}
	if marker := strings.Index(text, "```"); marker >= 0 && marker < end {
		tail := strings.ToLower(strings.TrimLeft(text[marker+3:], " \t\r\n"))
		if tail == "" || strings.HasPrefix("json", tail) || strings.HasPrefix(tail, "json") || strings.HasPrefix(tail, "{") {
			end = marker
		}
	}
	// A split opening fence is ambiguous until the next chunk arrives.
	if end == len(text) && strings.HasSuffix(text, "`") {
		end -= len(text) - len(strings.TrimRight(text, "`"))
	}
	return text[:end]
}

// finish replaces this request's preview with its complete sanitized prose, avoiding duplicate final text.
func (stream *agentTextStream) finish(text string) {
	stream.finished = true
	stream.publish(visibleAgentOutput(text))
}

// publish checkpoints visible partial output so cancellation and restart retain text already shown to the author.
func (stream *agentTextStream) publish(text string) {
	value := stream.base
	if text != "" {
		if value != "" && !stream.continuous {
			value += "\n\n"
		}
		value += text
	}
	stream.engine.jobsMu.Lock()
	defer stream.engine.jobsMu.Unlock()
	job := stream.engine.jobs[stream.jobID]
	if job == nil || job.Text == value && !(stream.writing && stream.finished) {
		return
	}
	job.Text = value
	if stream.writing && job.Writing != nil {
		job.Writing.Continuation = stream.prefix + text
		if stream.finished {
			job.Writing.Draft = stream.prefix + text
			job.Writing.Continuation = ""
			job.Writing.Phase = "reviewing"
			job.Writing.Review = nil
			job.Writing.CandidateBody, job.Writing.CandidateHash = "", ""
		}
	}
	if text != "" {
		job.Progress = "Writing the response…"
	}
	if time.Since(job.checkpointedAt) >= time.Second || stream.writing && stream.finished {
		job.checkpointedAt = time.Now()
		if err := stream.engine.store.SaveRun(job.ID, job.ProjectID, storedJob(job)); err != nil {
			job.Error = "Unable to save a recovery checkpoint: " + err.Error()
		}
	}
}

// visibleAgentOutput retains prose on both sides of complete tool envelopes and removes only protocol material.
func visibleAgentOutput(text string) string {
	remaining := text
	parts := []string{}
	removedProtocol := false
	for {
		object, start, end, found := extractBalancedJSON(remaining)
		if !found {
			if step, ok := recoverAgentStep(remaining); ok {
				removedProtocol = true
				prefix := remaining
				if at := strings.IndexByte(prefix, '{'); at >= 0 {
					prefix = trimAgentFence(prefix[:at])
				}
				parts = append(parts, prefix, visibleAgentProse(step))
			} else {
				parts = append(parts, remaining)
			}
			break
		}
		step, structured := recoverAgentStep(object)
		if !structured {
			parts = append(parts, remaining[:end])
			remaining = remaining[end:]
			continue
		}
		removedProtocol = true
		parts = append(parts, trimAgentFence(remaining[:start]), visibleAgentProse(step))
		remaining = strings.TrimPrefix(strings.TrimLeft(remaining[end:], " \t\r\n"), "```")
	}
	if !removedProtocol {
		return text
	}
	visible := []string{}
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			visible = append(visible, value)
		}
	}
	return strings.Join(visible, "\n\n")
}

// trimAgentFence removes an opening protocol fence adjacent to a recognized JSON envelope.
func trimAgentFence(text string) string {
	text = strings.TrimRight(text, " \t\r\n")
	for _, suffix := range []string{"```json", "```JSON", "```"} {
		if strings.HasSuffix(text, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(text, suffix))
		}
	}
	return text
}
