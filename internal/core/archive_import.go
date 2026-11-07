package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"unicode/utf8"
)

type archiveUTF8Reader struct {
	reader  io.Reader
	pending []byte
	failed  error
}

// Read validates UTF-8 across arbitrary chunk boundaries, including metadata keys read with Token.
func (reader *archiveUTF8Reader) Read(data []byte) (int, error) {
	if reader.failed != nil {
		return 0, reader.failed
	}
	count, err := reader.reader.Read(data)
	buffer := data[:count]
	if len(reader.pending) > 0 {
		buffer = append(reader.pending, buffer...)
	}
	reader.pending = nil
	for len(buffer) > 0 {
		if !utf8.FullRune(buffer) {
			reader.pending = append([]byte{}, buffer...)
			break
		}
		value, size := utf8.DecodeRune(buffer)
		if value == utf8.RuneError && size == 1 {
			reader.failed = errors.New("Invalid UTF-8 in backup")
			return 0, reader.failed
		}
		buffer = buffer[size:]
	}
	if errors.Is(err, io.EOF) && len(reader.pending) > 0 {
		reader.failed = errors.New("Incomplete UTF-8 in backup")
		return 0, reader.failed
	}
	return count, err
}

type archiveBudgetReader struct {
	reader    io.Reader
	remaining int64
	ctx       context.Context
}

// Read caps decoder growth for a single value even when the complete file is much larger.
func (reader *archiveBudgetReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if reader.remaining <= 0 {
		return 0, errors.New("One archive record exceeds the supported size")
	}
	if int64(len(data)) > reader.remaining {
		data = data[:reader.remaining]
	}
	count, err := reader.reader.Read(data)
	reader.remaining -= int64(count)
	return count, err
}

type archiveDecoder struct {
	decoder *json.Decoder
	reader  *archiveBudgetReader
}

// newArchiveDecoder limits allocation per value rather than imposing the old whole-file frame limit.
func newArchiveDecoder(ctx context.Context, reader io.Reader) *archiveDecoder {
	budget := &archiveBudgetReader{reader: &archiveUTF8Reader{reader: reader}, remaining: archiveRecordBytes, ctx: ctx}
	return &archiveDecoder{decoder: json.NewDecoder(budget), reader: budget}
}

// reset accounts for decoder read-ahead before admitting another bounded JSON value.
func (input *archiveDecoder) reset() error {
	buffered, err := io.Copy(io.Discard, input.decoder.Buffered())
	if err != nil {
		return err
	}
	input.reader.remaining = archiveRecordBytes - buffered
	if input.reader.remaining < 0 {
		return errors.New("One archive record exceeds the supported size")
	}
	return nil
}

// token reads structural JSON without allowing unlimited whitespace or key allocation.
func (input *archiveDecoder) token() (json.Token, error) {
	if err := input.reset(); err != nil {
		return nil, err
	}
	return input.decoder.Token()
}

// more reports whether an object or array contains another member, preserving decoder errors.
func (input *archiveDecoder) more() (bool, error) {
	if err := input.reset(); err != nil {
		return false, err
	}
	return input.decoder.More(), nil
}

// decode reads a bounded record and verifies its exact consumed size independently of read-ahead.
func (input *archiveDecoder) decode(value any) error {
	if err := input.reset(); err != nil {
		return err
	}
	start := input.decoder.InputOffset()
	if err := input.decoder.Decode(value); err != nil {
		return err
	}
	if input.decoder.InputOffset()-start > archiveRecordBytes {
		return errors.New("One archive record exceeds the supported size")
	}
	return nil
}

// delimiter enforces the expected JSON structure instead of silently accepting an incomplete archive.
func (input *archiveDecoder) delimiter(expected json.Delim) error {
	token, err := input.token()
	if err != nil {
		return err
	}
	if token != expected {
		return errors.New("The backup has an invalid structure")
	}
	return nil
}

// readArchiveArray visits one typed record at a time while preserving non-null collection validation.
func readArchiveArray[T any](input *archiveDecoder, consume func(T) error) error {
	token, err := input.token()
	if err != nil {
		return err
	}
	if token != json.Delim('[') {
		return errors.New("The backup collection is not an array")
	}
	for {
		more, err := input.more()
		if err != nil {
			return err
		}
		if !more {
			break
		}
		var value T
		if err := input.decode(&value); err != nil {
			return err
		}
		if err := consume(value); err != nil {
			return err
		}
	}
	return input.delimiter(']')
}

type archiveRuntimeRecord struct {
	Kind    string          `json:"kind"`
	Session string          `json:"session,omitempty"`
	Value   json.RawMessage `json:"value"`
}
type stagedArchive struct {
	project       Project
	spool         *os.File
	encoder       *json.Encoder
	sessions      map[string]Conversation
	sessionOrder  []string
	sessionIDs    map[string]string
	sessionTokens map[string]string
	turnIDs       map[string]string
	jobIDs        map[string]string
	experiments   int
}

// stageRecord stores runtime data on disk so imported prompt history is never collected into a Project slice.
func (stage *stagedArchive) stageRecord(kind, session string, value any) error {
	data, err := jsonBytes(value)
	if err != nil {
		return err
	}
	return stage.encoder.Encode(archiveRuntimeRecord{Kind: kind, Session: session, Value: data})
}

// readConversation streams turns independently and records metadata after all keys have been read.
func (stage *stagedArchive) readConversation(input *archiveDecoder) error {
	if err := input.delimiter('{'); err != nil {
		return err
	}
	tokenID := newID()
	fields := map[string]json.RawMessage{}
	count := 0
	for {
		more, err := input.more()
		if err != nil {
			return err
		}
		if !more {
			break
		}
		token, err := input.token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("Invalid conversation field")
		}
		if _, duplicate := fields[key]; duplicate {
			return errors.New("Duplicate conversation field")
		}
		if key == "turns" {
			fields[key] = json.RawMessage("[]")
			if err := readArchiveArray[ConversationTurn](input, func(turn ConversationTurn) error {
				count++
				if count > 20000 || turn.ID == "" || !contains([]string{"user", "assistant"}, turn.Role) {
					return errors.New("Invalid or oversized archived conversation")
				}
				if _, exists := stage.turnIDs[turn.ID]; exists {
					return errors.New("Duplicate conversation turn ID")
				}
				stage.turnIDs[turn.ID] = newID()
				return stage.stageRecord("turn", tokenID, turn)
			}); err != nil {
				return err
			}
		} else {
			var raw json.RawMessage
			if err := input.decode(&raw); err != nil {
				return err
			}
			fields[key] = raw
		}
	}
	if err := input.delimiter('}'); err != nil {
		return err
	}
	var session Conversation
	if err := decodeJSON(rawMessage(fields), &session); err != nil {
		return err
	}
	if session.ID == "" || len(stage.sessions) >= 20000 {
		return errors.New("Invalid or oversized archived conversation")
	}
	if _, exists := stage.sessions[session.ID]; exists {
		return errors.New("Duplicate conversation ID")
	}
	stage.sessions[session.ID] = session
	stage.sessionOrder = append(stage.sessionOrder, session.ID)
	stage.sessionIDs[session.ID] = newID()
	stage.sessionTokens[tokenID] = session.ID
	return nil
}

// readRuntime streams tasks, conversations and experiments into the import spool with count limits.
func (stage *stagedArchive) readRuntime(input *archiveDecoder, key string) error {
	switch key {
	case "jobs":
		return readArchiveArray[Job](input, func(job Job) error {
			if job.ID == "" || len(stage.jobIDs) >= 20000 {
				return errors.New("Invalid or oversized archived task history")
			}
			if _, exists := stage.jobIDs[job.ID]; exists {
				return errors.New("Duplicate archived task ID")
			}
			stage.jobIDs[job.ID] = newID()
			return stage.stageRecord("job", "", job)
		})
	case "experiments":
		return readArchiveArray[PolicyExperiment](input, func(experiment PolicyExperiment) error {
			stage.experiments++
			if stage.experiments > 200 {
				return errors.New("Too many experiments in the backup")
			}
			if err := validateExperiment(experiment); err != nil {
				return err
			}
			return stage.stageRecord("experiment", "", experiment)
		})
	case "conversations":
		token, err := input.token()
		if err != nil {
			return err
		}
		if token != json.Delim('[') {
			return errors.New("Invalid conversation collection")
		}
		for {
			more, err := input.more()
			if err != nil {
				return err
			}
			if !more {
				break
			}
			if err := stage.readConversation(input); err != nil {
				return err
			}
		}
		return input.delimiter(']')
	}
	return errors.New("Unknown runtime collection")
}

// readStudio keeps author settings in memory while parsing each bounded memory and plan independently.
func (stage *stagedArchive) readStudio(input *archiveDecoder) (json.RawMessage, error) {
	if err := input.delimiter('{'); err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	for {
		more, err := input.more()
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
		token, err := input.token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("Invalid studio field")
		}
		if _, exists := fields[key]; exists {
			return nil, errors.New("Duplicate studio field")
		}
		fields[key] = json.RawMessage("[]")
		switch key {
		case "locations":
			err = readArchiveArray[WorldLocation](input, func(location WorldLocation) error {
				stage.project.Studio.Locations = append(stage.project.Studio.Locations, location)
				if len(stage.project.Studio.Locations) > 10000 {
					return errors.New("Too many map locations")
				}
				return nil
			})
		case "routes":
			err = readArchiveArray[WorldRoute](input, func(route WorldRoute) error {
				stage.project.Studio.Routes = append(stage.project.Studio.Routes, route)
				if len(stage.project.Studio.Routes) > 30000 {
					return errors.New("Too many map routes")
				}
				return nil
			})
		case "changesets":
			err = readArchiveArray[ChangeSet](input, func(cs ChangeSet) error {
				if err := validateChangeSet(&cs); err != nil {
					return err
				}
				stage.project.Studio.ChangeSets = append(stage.project.Studio.ChangeSets, cs)
				if len(stage.project.Studio.ChangeSets) > 20000 {
					return errors.New("Too many reviewed changes")
				}
				return nil
			})
		case "plans":
			err = readArchiveArray[StoryPlan](input, func(plan StoryPlan) error {
				stage.project.Studio.Plans = append(stage.project.Studio.Plans, plan)
				if len(stage.project.Studio.Plans) > 100000 {
					return errors.New("Too many plans")
				}
				return nil
			})
		case "memories":
			err = readArchiveArray[StoryMemory](input, func(memory StoryMemory) error {
				stage.project.Studio.Memories = append(stage.project.Studio.Memories, memory)
				if len(stage.project.Studio.Memories) > 100000 {
					return errors.New("Too many memories")
				}
				return nil
			})
		case "decisions":
			err = readArchiveArray[StoryDecision](input, func(decision StoryDecision) error {
				stage.project.Studio.Decisions = append(stage.project.Studio.Decisions, decision)
				if len(stage.project.Studio.Decisions) > 20000 {
					return errors.New("Too many decisions")
				}
				return nil
			})
		default:
			var raw json.RawMessage
			err = input.decode(&raw)
			fields[key] = raw
		}
		if err != nil {
			return nil, err
		}
	}
	if err := input.delimiter('}'); err != nil {
		return nil, err
	}
	return jsonBytes(fields)
}

// readProject streams collection fields while retaining the compatible metadata decoder and defaults.
func (stage *stagedArchive) readProject(input *archiveDecoder) error {
	if err := input.delimiter('{'); err != nil {
		return err
	}
	metadata := map[string]json.RawMessage{}
	for {
		more, err := input.more()
		if err != nil {
			return err
		}
		if !more {
			break
		}
		token, err := input.token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("Invalid project field")
		}
		if _, exists := metadata[key]; exists {
			return errors.New("Duplicate project field")
		}
		metadata[key] = json.RawMessage("[]")
		switch key {
		case "chapters":
			err = readArchiveArray[Chapter](input, func(value Chapter) error { stage.project.Chapters = append(stage.project.Chapters, value); return nil })
		case "entities":
			err = readArchiveArray[Entity](input, func(value Entity) error { stage.project.Entities = append(stage.project.Entities, value); return nil })
		case "facts":
			err = readArchiveArray[Fact](input, func(value Fact) error { stage.project.Facts = append(stage.project.Facts, value); return nil })
		case "sources":
			err = readArchiveArray[Source](input, func(value Source) error { stage.project.Sources = append(stage.project.Sources, value); return nil })
		case "compilations":
			err = readArchiveArray[WikiCompilation](input, func(value WikiCompilation) error {
				stage.project.Compilations = append(stage.project.Compilations, value)
				return nil
			})
		case "studio":
			metadata[key], err = stage.readStudio(input)
		case "jobs", "conversations", "experiments":
			err = stage.readRuntime(input, key)
		default:
			var raw json.RawMessage
			err = input.decode(&raw)
			metadata[key] = raw
		}
		if err != nil {
			return err
		}
	}
	if err := input.delimiter('}'); err != nil {
		return err
	}
	var project Project
	if err := decodeJSON(rawMessage(metadata), &project); err != nil {
		return err
	}
	project.Chapters, project.Entities, project.Facts, project.Sources, project.Compilations = stage.project.Chapters, stage.project.Entities, stage.project.Facts, stage.project.Sources, stage.project.Compilations
	project.Studio.Plans, project.Studio.Memories, project.Studio.Decisions = stage.project.Studio.Plans, stage.project.Studio.Memories, stage.project.Studio.Decisions
	project.Studio.Locations, project.Studio.Routes, project.Studio.ChangeSets = stage.project.Studio.Locations, stage.project.Studio.Routes, stage.project.Studio.ChangeSets
	project.Conversations, project.ArchiveJobs, project.Experiments = nil, nil, nil
	stage.project = project
	return validateProject(&stage.project)
}

// parseArchive checks the complete envelope before any live project data is inserted.
func (stage *stagedArchive) parseArchive(input *archiveDecoder) error {
	if err := input.delimiter('{'); err != nil {
		return err
	}
	format := ""
	seen := map[string]bool{}
	for {
		more, err := input.more()
		if err != nil {
			return err
		}
		if !more {
			break
		}
		token, err := input.token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return errors.New("Invalid or duplicate backup field")
		}
		seen[key] = true
		switch key {
		case "format":
			err = input.decode(&format)
		case "project":
			err = stage.readProject(input)
		default:
			var raw json.RawMessage
			err = input.decode(&raw)
		}
		if err != nil {
			return err
		}
	}
	if err := input.delimiter('}'); err != nil {
		return err
	}
	if !contains([]string{"quillweave-1", "quillweave-2"}, format) || !seen["project"] {
		return errors.New("Unsupported or incomplete backup format")
	}
	var extra any
	if err := input.decode(&extra); err != io.EOF {
		return errors.New("The backup contains trailing data")
	}
	return nil
}

// insertSessions restores metadata before turn records, allowing either collection order in older JSON files.
func (stage *stagedArchive) insertSessions(connection *sql.Conn, oldProjectID string) error {
	for _, oldID := range stage.sessionOrder {
		session := stage.sessions[oldID]
		if session.ProjectID != oldProjectID {
			return errors.New("Archived conversation belongs to another project")
		}
		if session.ChapterID != "" {
			if _, _, ok := stage.project.SourceText(session.ChapterID); !ok {
				return errors.New("Archived conversation source not found")
			}
		}
		session.ID, session.ProjectID = stage.sessionIDs[oldID], stage.project.ID
		session.ParentID, session.ForkTurnID = stage.sessionIDs[session.ParentID], stage.turnIDs[session.ForkTurnID]
		session.Revision, session.UpdatedAt = 1, now()
		if session.ChapterID != "" {
			session.OriginChapterID, session.ChapterID = session.ChapterID, ""
		}
		session.Checkpoint = nil
		session.BibleBaseline, session.LastBibleHash, session.Summary, session.CompactedSummary = "", "", "", ""
		session.CompactedUpToOrdinal = 0
		session.Turns = []ConversationTurn{}
		if _, err := connection.ExecContext(context.Background(), "INSERT INTO conversations(id,project_id,chapter_id,document) VALUES(?,?,?,?)", session.ID, session.ProjectID, session.ChapterID, jsonText(session)); err != nil {
			return err
		}
		stage.sessions[oldID] = session
	}
	return nil
}

// insertRuntime rekeys every runtime reference and writes from the disk spool within the project transaction.
func (stage *stagedArchive) insertRuntime(connection *sql.Conn, oldProjectID string) error {
	if _, err := stage.spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	decoder := json.NewDecoder(stage.spool)
	ordinals := map[string]int{}
	for {
		var record archiveRuntimeRecord
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch record.Kind {
		case "turn":
			var turn ConversationTurn
			if err := decodeJSON(record.Value, &turn); err != nil {
				return err
			}
			oldID := stage.sessionTokens[record.Session]
			session := stage.sessions[oldID]
			turn.ID, turn.JobID, turn.RequestID = stage.turnIDs[turn.ID], stage.jobIDs[turn.JobID], ""
			if turn.Status == "running" {
				turn.Status = "interrupted"
			}
			if !contains([]string{"done", "error", "cancelled", "interrupted"}, turn.Status) {
				return errors.New("Invalid archived turn status")
			}
			if _, err := connection.ExecContext(context.Background(), "INSERT INTO conversation_turns(id,session_id,ordinal,document) VALUES(?,?,?,?)", turn.ID, session.ID, ordinals[session.ID], jsonText(turn)); err != nil {
				return err
			}
			ordinals[session.ID]++
			if err := indexConversationTurn(connection, &session, turn); err != nil {
				return err
			}
		case "job":
			var job Job
			if err := decodeJSON(record.Value, &job); err != nil {
				return err
			}
			if job.ProjectID != oldProjectID {
				return errors.New("Archived task belongs to another project")
			}
			job.ID, job.ProjectID, job.SessionID = stage.jobIDs[job.ID], stage.project.ID, stage.sessionIDs[job.SessionID]
			for i := range job.ChangeSets {
				rekeyChangeSet(&job.ChangeSets[i], stage.project.ID, stage.sessionIDs, stage.turnIDs, stage.jobIDs)
			}
			if job.Status == "running" {
				job.Status = "interrupted"
			}
			if !contains([]string{"done", "error", "cancelled", "interrupted"}, job.Status) {
				return errors.New("Invalid archived task status")
			}
			for i := range job.Prompt.Messages {
				job.Prompt.Messages[i].SourceJobID = stage.jobIDs[job.Prompt.Messages[i].SourceJobID]
			}
			if _, err := connection.ExecContext(context.Background(), "INSERT INTO runs(id,project_id,document) VALUES(?,?,?)", job.ID, job.ProjectID, jsonText(job)); err != nil {
				return err
			}
		case "experiment":
			var experiment PolicyExperiment
			if err := decodeJSON(record.Value, &experiment); err != nil {
				return err
			}
			experiment.ID = newID()
			if _, err := connection.ExecContext(context.Background(), "INSERT INTO experiments(id,project_id,document) VALUES(?,?,?)", experiment.ID, stage.project.ID, jsonText(experiment)); err != nil {
				return err
			}
		default:
			return errors.New("Invalid staged runtime record")
		}
	}
	return nil
}

// importArchiveFrom parses on disk, then publishes the new book and all runtime records atomically.
func (engine *Engine) importArchiveFrom(reader io.Reader) (Project, error) {
	file, err := os.CreateTemp(engine.owner.dir, "import-runtime-*")
	if err != nil {
		return Project{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	stage := stagedArchive{project: Project{Chapters: []Chapter{}, Entities: []Entity{}, Facts: []Fact{}, Sources: []Source{}, Compilations: []WikiCompilation{}, Studio: defaultStudio()}, spool: file, encoder: json.NewEncoder(file), sessions: map[string]Conversation{}, sessionIDs: map[string]string{}, sessionTokens: map[string]string{}, turnIDs: map[string]string{}, jobIDs: map[string]string{}}
	stage.encoder.SetEscapeHTML(false)
	if err := stage.parseArchive(newArchiveDecoder(engine.ctx, reader)); err != nil {
		return Project{}, err
	}
	oldID := stage.project.ID
	stage.project.ID, stage.project.Revision, stage.project.UpdatedAt = newID(), 0, now()
	for i := range stage.project.Studio.ChangeSets {
		rekeyChangeSet(&stage.project.Studio.ChangeSets[i], stage.project.ID, stage.sessionIDs, stage.turnIDs, stage.jobIDs)
	}
	for i := range stage.project.Studio.Memories {
		stage.project.Studio.Memories[i].SessionID = stage.sessionIDs[stage.project.Studio.Memories[i].SessionID]
	}
	for i := range stage.project.Studio.Decisions {
		stage.project.Studio.Decisions[i].SessionID = stage.sessionIDs[stage.project.Studio.Decisions[i].SessionID]
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return Project{}, errors.New("The application is closing")
	}
	err = engine.store.withWriteTransaction(func(connection *sql.Conn) error {
		if err := insertProjectIn(connection, &stage.project); err != nil {
			return err
		}
		if err := stage.insertSessions(connection, oldID); err != nil {
			return err
		}
		return stage.insertRuntime(connection, oldID)
	})
	return stage.project, err
}
