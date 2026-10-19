package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
)

const finalQuiesceSource = "final_quiesce_ci.go"

var finalQuiesceBodies = map[string]struct{ file, body string }{
	"ConfigureCIShutdown": {"final_quiesce_ci.go", `{
	coordinator.Lock()
	defer coordinator.Unlock()
	g := coordinator.gate
	if g == nil || !g.application || stop == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finalQuiesce.stop != nil {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return
	}
	g.finalQuiesce.stop = stop
}`},
	"lifecycleGate.finalDrainedLocked": {"final_quiesce_ci.go", `{
	if g.invalid || g.pending != 0 || g.open != 0 {
		return false
	}
	for _, t := range g.tickets {
		if t.state == 'p' || t.state == 'o' || t.state == 'x' || t.state == 'q' || t.operationInFlight != 0 {
			return false
		}
	}
	return true
}`},
	"lifecycleGate.prepareFinalQuiesce": {"final_quiesce_ci.go", `{
	g.mu.Lock()
	if budgetMillis <= 0 || budgetMillis > 30000 || g.finalQuiesce.state != "" || g.finalQuiesce.stop == nil || g.workerContext == nil || g.workerContext.Err() != nil || g.seal == nil || !g.seal.released || !g.seal.recoveryAuthorized || g.invalid {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return errSealProtocol
	}
	g.finalQuiesce.state = "parking"
	g.finalQuiesce.deadline = time.Now().Add(time.Duration(budgetMillis) * time.Millisecond)
	g.finalParked = true
	g.notifyLocked()
	for {
		if g.invalid || g.workerContext.Err() != nil || !time.Now().Before(g.finalQuiesce.deadline) {
			g.finalQuiesce.state = "failed"
			g.invalidateBecauseLocked(invalidExplicit)
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return errSealProtocol
		}
		if g.finalDrainedLocked() {
			g.finalQuiesce.state = "drained"
			g.mu.Unlock()
			return nil
		}
		changed := g.changed
		remaining := time.Until(g.finalQuiesce.deadline)
		g.mu.Unlock()
		timer := time.NewTimer(remaining)
		select {
		case <-changed:
		case <-timer.C:
		case <-g.workerContext.Done():
		}
		timer.Stop()
		g.mu.Lock()
	}
}`},
	"lifecycleGate.cancelFinalQuiesce": {"final_quiesce_ci.go", `{
	g.mu.Lock()
	if g.finalQuiesce.state != "drained" || !g.finalParked || g.finalQuiesce.stop == nil || g.workerContext == nil || g.workerContext.Err() != nil || !time.Now().Before(g.finalQuiesce.deadline) || !g.finalDrainedLocked() {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return errSealProtocol
	}
	g.finalQuiesce.state = "cancelling"
	stop := g.finalQuiesce.stop
	g.mu.Unlock()
	// The installed logical root CancelFunc returns only after synchronous child
	// cancellation propagation. Observing root.Done alone does not supply this.
	stop()
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if g.invalid || g.workerContext.Err() != context.Canceled || !time.Now().Before(g.finalQuiesce.deadline) {
		g.finalQuiesce.state = "failed"
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return errSealProtocol
	}
	g.finalQuiesce.state = "cancelled"
	g.notifyLocked()
	return nil
}`},
	"lifecycleGate.abortFinalQuiesce": {"final_quiesce_ci.go", `{
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if !g.finalParked {
		return
	}
	g.invalidateBecauseLocked(invalidExplicit)
	g.finalQuiesce.state = "failed"
	g.finalParked = false
	g.notifyLocked()
}`},
	"lifecycleGate.completeFinalQuiesce": {"final_quiesce_ci.go", `{
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if g.finalQuiesce.state != "cancelled" || !g.finalParked || g.invalid || !g.finalDrainedLocked() || !time.Now().Before(g.finalQuiesce.deadline) {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return errSealProtocol
	}
	g.finalQuiesce.state = "complete"
	g.finalParked = false
	g.notifyLocked()
	return nil
}`},
	"StartCIController": {"application_controller_ci.go", `{
	coordinator.Lock()
	defer coordinator.Unlock()
	if coordinator.used || coordinator.gate != nil || normal == nil || rescue == nil || witness == nil || captureEnd == nil {
		return nil, errSealProtocol
	}
	endFD, err := prepareCaptureEnd(captureEnd)
	if err != nil {
		return nil, err
	}
	w, err := mapCIWitness(witness, nonce)
	if err != nil {
		return nil, err
	}
	g := &lifecycleGate{witness: w, application: true, nonce: nonce, captureEndFD: endFD}
	coordinator.used = true
	coordinator.gate = g
	var stopping atomic.Bool
	var stopOnce, reportOnce, recoveryOnce sync.Once
	var wg sync.WaitGroup
	cleanup := func() {
		g.finalTransportMu.Lock()
		stopOnce.Do(func() { stopping.Store(true); normal.Close(); rescue.Close() })
		g.finalTransportMu.Unlock()
		wg.Wait()
		recoveryOnce.Do(func() { g.finishRecoveryCleanup() })
		// Retain the reader through controller join and serialize raw-FD teardown
		// with every consumer, so a late callback cannot read a reused descriptor.
		g.mu.Lock()
		g.captureEndFD = 0
		captureEnd.Close()
		g.mu.Unlock()
		reportOnce.Do(func() { g.writeFinalInvalidationDiagnostic(); g.writeRecoveryCleanupDiagnostic() })
	}
	// Each channel has one reader. Normal EOF cannot close the rescue channel.
	wg.Add(2)
	go func() { defer wg.Done(); serveApplicationNormal(normal, g, &stopping, onFailure) }()
	go func() { defer wg.Done(); serveApplicationRescue(rescue, g, &stopping, onFailure) }()
	return cleanup, nil
}`},
	"readApplicationRequest": {"application_controller_ci.go", `{
	var r applicationRequest
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return r, err
	}
	if len(line) > limit {
		return r, errSealProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil {
		return r, errSealProtocol
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || r.Version != 2 || r.Epoch != 1 || r.Seq <= last {
		return r, errSealProtocol
	}
	// JSON duplicate keys are not permitted, even when values are equal.
	var fields map[string]json.RawMessage
	if json.Unmarshal(line, &fields) != nil {
		return r, errSealProtocol
	}
	d := json.NewDecoder(bytes.NewReader(line))
	if _, err = d.Token(); err != nil {
		return r, errSealProtocol
	}
	seen := make(map[string]bool)
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return r, errSealProtocol
		}
		s, ok := key.(string)
		if !ok || seen[s] {
			return r, errSealProtocol
		}
		seen[s] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return r, errSealProtocol
		}
	}
	expected := map[string]bool{"version": true, "epoch": true, "seq": true, "op": true}
	switch r.Op {
	case "final_prepare":
		expected["budgetMillis"] = true
	case "pin":
		expected["target"] = true
	case "prepare_selection":
		expected["kind"] = true
		expected["budgetMillis"] = true
	case "configure_target":
		expected["taskID"], expected["workerID"] = true, true
	}
	if len(fields) != len(expected) {
		return r, errSealProtocol
	}
	for field := range fields {
		if !expected[field] {
			return r, errSealProtocol
		}
	}
	return r, nil
}`},
	"recordApplicationFailure": {"application_controller_ci.go", `{
	// Only the actual local Close error may be suppressed. A previously observed
	// EOF/partial/timeout cannot be relabelled by a later cleanup Close.
	if !(stopping.Load() && errors.Is(original, os.ErrClosed)) {
		g.invalidateTransport()
		if onFailure != nil {
			onFailure()
		}
	}
	file.Close()
}`},
	"serveApplicationNormal": {"application_controller_ci.go", `{
	var writeMu sync.Mutex
	send := func(seq uint64, kind string) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		f, err := g.applicationSnapshot(seq, kind)
		if err != nil {
			return err
		}
		return writeApplicationFrame(file, f, 65536)
	}
	terminal := error(errSealProtocol)
	defer func() { recordApplicationFailure(file, g, stopping, onFailure, terminal) }()
	if err := send(0, "ready"); err != nil {
		terminal = err
		return
	}
	reader := bufio.NewReaderSize(file, 1024)
	var last uint64
	for {
		r, err := readApplicationRequest(reader, 1024, last)
		if err != nil {
			terminal = err
			return
		}
		last = r.Seq
		if r.Op != "prepare_selection" && r.Op != "configure_target" && (r.Kind != "" || r.TaskID != "" || r.WorkerID != "") {
			return
		}
		if r.Op != "final_prepare" && r.Op != "prepare_selection" && r.BudgetMillis != 0 {
			return
		}
		switch r.Op {
		case "final_prepare":
			if r.Target != 0 || g.prepareFinalQuiesce(r.BudgetMillis) != nil {
				return
			}
			writeMu.Lock()
			err := writeFinalQuiesceFrame(file, g, r.Seq, "drained")
			writeMu.Unlock()
			if err != nil {
				terminal = err
				return
			}
			continue
		case "final_cancel":
			if r.Target != 0 || !g.finalTransportMu.TryLock() {
				return
			}
			err := func() error {
				defer g.finalTransportMu.Unlock()
				if stopping.Load() {
					return errSealProtocol
				}
				if err := g.cancelFinalQuiesce(); err != nil {
					return err
				}
				writeMu.Lock()
				err := writeFinalQuiesceFrame(file, g, r.Seq, "cancelled")
				writeMu.Unlock()
				if err != nil {
					g.abortFinalQuiesce()
					return err
				}
				return g.completeFinalQuiesce()
			}()
			if err != nil {
				terminal = err
				return
			}
			continue
		case "prepare_selection":
			if r.Target != 0 || r.TaskID != "" || r.WorkerID != "" || g.prepareQueuedSelection(r.Kind, r.BudgetMillis) != nil {
				return
			}
		case "configure_target":
			if r.Target != 0 || r.Kind != "" || g.configureActivitySelection(r.TaskID, r.WorkerID) != nil {
				return
			}
		case "diagnostic":
			if r.Target != 0 {
				return
			}
			writeMu.Lock()
			err := writeApplicationFrame(file, g.applicationDiagnostic(r.Seq), 1024)
			writeMu.Unlock()
			if err != nil {
				terminal = err
				return
			}
			continue
		case "snapshot", "status":
			if r.Target != 0 {
				return
			}
		case "pin":
			if g.pin(1, r.Target, func() {
				if err := send(0, "invalid"); err != nil {
					recordApplicationFailure(file, g, stopping, onFailure, err)
				}
			}) != nil {
				return
			}
		default:
			return
		}
		if err := send(r.Seq, "reply"); err != nil {
			terminal = err
			return
		}
		if r.Op == "configure_target" {
			if err := g.completeQueuedSelection(); err != nil {
				terminal = err
				return
			}
		}
	}
}`},
	"serveApplicationRescue": {"application_controller_ci.go", `{
	terminal := error(errSealProtocol)
	defer func() { recordApplicationFailure(file, g, stopping, onFailure, terminal) }()
	reader := bufio.NewReaderSize(file, 256)
	var last uint64
	for {
		r, err := readApplicationRequest(reader, 256, last)
		if err != nil {
			terminal = err
			return
		}
		last = r.Seq
		if r.Target != 0 || r.Kind != "" || r.TaskID != "" || r.WorkerID != "" || r.BudgetMillis != 0 {
			return
		}
		switch r.Op {
		case "release":
			if g.release(1) != nil {
				return
			}
		case "abort":
			g.abortFinalQuiesce()
			g.abortQueuedSelection()
			g.mu.Lock()
			g.invalidateBecauseLocked(invalidAbort)
			if g.seal != nil {
				g.seal.released = true
			}
			g.closing = false
			g.notifyLocked()
			g.mu.Unlock()
		default:
			return
		}
		ack := struct {
			Version int    ` + "`" + `json:"version"` + "`" + `
			Epoch   uint64 ` + "`" + `json:"epoch"` + "`" + `
			Seq     uint64 ` + "`" + `json:"seq"` + "`" + `
			Kind    string ` + "`" + `json:"kind"` + "`" + `
			Status  string ` + "`" + `json:"status"` + "`" + `
		}{2, 1, r.Seq, "reply", "released"}
		if err := writeApplicationFrame(file, ack, 1024); err != nil {
			terminal = err
			return
		}
	}
}`},
	"writeFinalQuiesceFrame": {"application_controller_ci.go", `{
	g.mu.Lock()
	deadline := g.finalQuiesce.deadline
	valid := !g.invalid && g.finalParked && g.finalDrainedLocked() && g.finalQuiesce.state == status
	g.mu.Unlock()
	if !valid || !time.Now().Before(deadline) {
		return errSealProtocol
	}
	frame := struct {
		Version int    ` + "`" + `json:"version"` + "`" + `
		Epoch   uint64 ` + "`" + `json:"epoch"` + "`" + `
		Seq     uint64 ` + "`" + `json:"seq"` + "`" + `
		Kind    string ` + "`" + `json:"kind"` + "`" + `
		Status  string ` + "`" + `json:"status"` + "`" + `
		Pending int    ` + "`" + `json:"pending"` + "`" + `
		Open    int    ` + "`" + `json:"open"` + "`" + `
	}{2, 1, seq, "quiesce", status, 0, 0}
	data, err := json.Marshal(frame)
	if err != nil || len(data)+1 > 1024 {
		return errSealProtocol
	}
	data = append(data, '\n')
	if bound := time.Now().Add(500 * time.Millisecond); bound.Before(deadline) {
		deadline = bound
	}
	if err := file.SetWriteDeadline(deadline); err != nil {
		return err
	}
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return errSealProtocol
	}
	return nil
}`},
}

const finalQuiesceType = "struct {\n\tstate    string\n\tdeadline time.Time\n\tstop     context.CancelFunc\n}"

func exactFinalQuiesceFunction(path, file, name string, fn *ast.FuncDecl) bool {
	expected, ok := finalQuiesceBodies[name]
	if !ok || path != managed || file != expected.file || fn == nil || fn.Body == nil {
		return false
	}
	if name == "readApplicationRequest" {
		// Repository parsing attaches the duplicate-key comment to a local
		// GenDecl.Doc. Normalize only this reader to the comment-free parse mode
		// used by its fixed body, retaining every statement and literal value.
		parsed, err := parser.ParseFile(token.NewFileSet(), "actual.go", "package expected;func expected()"+sourceText(fn.Body), 0)
		if err != nil {
			return false
		}
		fn = parsed.Decls[0].(*ast.FuncDecl)
	}
	return captureBodyMatches(fn, expected.body)
}
func finalQuiesceDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
	var out []Diagnostic
	emit := func(n ast.Node, reason string) { out = append(out, Diagnostic{fset.Position(n.Pos()), reason}) }
	for _, file := range files {
		filename := filepath.Base(fset.Position(file.Pos()).Filename)
		for _, decl := range file.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			name := ""
			if fn != nil {
				name = fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) > 0 {
					name = receiverName(fn.Recv.List[0].Type) + "." + name
				}
			}
			exact := exactFinalQuiesceFunction(path, filename, name, fn)
			if expected, ok := finalQuiesceBodies[name]; ok && path == managed && (expected.file == finalQuiesceSource || name == "writeFinalQuiesceFrame" || name == "readApplicationRequest") && !exact {
				emit(decl, "final quiescence must retain its exact drain, cancellation barrier and ACK bodies")
			}
			var ancestors []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				switch x := n.(type) {
				case *ast.TypeSpec:
					if path == managed && x.Name.Name == "finalQuiesce" {
						expected, err := parser.ParseFile(token.NewFileSet(), "expected.go", "package expected;type expected "+finalQuiesceType, 0)
						if filename != finalQuiesceSource || err != nil || compactSource(x.Type) != compactSource(expected.Decls[0].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type) {
							emit(x, "final quiescence state must remain private and exactly typed")
						}
					}
				case *ast.SelectorExpr:
					selected := info.Selections[x]
					if selected != nil && selected.Kind() == types.FieldVal && finalQuiesceField(selected.Recv(), selected.Obj().Name()) && !exact && !(name == "lifecycleGate.acquire" && exactRecoveryFunction(path, filename, name, fn)) && !exactTransportInvalidation(path, filename, name, fn) && !finalTransportCleanupField(path, filename, name, x, ancestors) {
						emit(x, "final shutdown state or CancelFunc outside exact protocol ownership")
					}
				case *ast.CompositeLit:
					if isNamed(info.TypeOf(x), managed, "finalQuiesce") {
						emit(x, "final shutdown state cannot be preinitialized")
					}
					if isNamed(info.TypeOf(x), managed, "lifecycleGate") {
						for _, element := range x.Elts {
							if kv, ok := element.(*ast.KeyValueExpr); ok {
								if key, ok := kv.Key.(*ast.Ident); ok && finalQuiesceField(info.TypeOf(x), key.Name) {
									emit(kv, "final shutdown gate fields cannot be preinitialized")
								}
							}
						}
					}
				case *ast.StarExpr:
					if info.Types[x].IsValue() && isNamed(info.TypeOf(x), managed, "finalQuiesce") {
						emit(x, "final shutdown state cannot be copied or replaced")
					}
				case *ast.Ident:
					obj, ok := info.Uses[x].(*types.Func)
					if !ok || obj.Pkg() == nil || obj.Pkg().Path() != managed {
						break
					}
					allowed := false
					protected := true
					switch obj.Name() {
					case "ConfigureCIShutdown":
						allowed = path == module+"/cmd/adtr" && filename == "db_observation_ci.go" && name == "observeCIWorkerContext" && captureBodyMatches(fn, `{dbadmission.ObserveCIWorkerContext(ctx);dbadmission.ConfigureCIShutdown(stop)}`) && directCallFor(x, ancestors) != nil
					case "prepareFinalQuiesce":
						allowed = exact && name == "serveApplicationNormal" && directCallFor(x, ancestors) != nil
					case "cancelFinalQuiesce", "completeFinalQuiesce":
						allowed = exact && name == "serveApplicationNormal" && captureConsumerCall(x, ancestors, true)
					case "abortFinalQuiesce":
						allowed = exact && (name == "serveApplicationNormal" || name == "serveApplicationRescue") && captureConsumerCall(x, ancestors, true)
					case "finalDrainedLocked":
						allowed = (exact && (name == "lifecycleGate.prepareFinalQuiesce" || name == "lifecycleGate.cancelFinalQuiesce" || name == "lifecycleGate.completeFinalQuiesce" || name == "writeFinalQuiesceFrame") || exactQueuedParkFunction(path, filename, name, fn) && (name == "lifecycleGate.prepareQueuedSelection" || name == "lifecycleGate.completeQueuedSelection" || name == "lifecycleGate.configureActivitySelection")) && directCallFor(x, ancestors) != nil
					case "writeFinalQuiesceFrame":
						allowed = exact && name == "serveApplicationNormal" && finalFrameCall(x, ancestors)
					default:
						protected = false
					}
					if protected && !allowed {
						emit(x, "final quiescence operation outside its exact synchronous protocol caller")
					}
				}
				return true
			})
		}
	}
	return out
}
func finalQuiesceField(typ types.Type, name string) bool {
	return isNamed(typ, managed, "finalQuiesce") || (isNamed(typ, managed, "lifecycleGate") && (name == "finalQuiesce" || name == "finalParked" || name == "finalTransportMu"))
}
func finalFrameCall(id *ast.Ident, ancestors []ast.Node) bool {
	if len(ancestors) < 2 {
		return false
	}
	call, ok := ancestors[len(ancestors)-2].(*ast.CallExpr)
	if !ok || call.Fun != id || len(call.Args) != 4 || call.Ellipsis.IsValid() {
		return false
	}
	for _, node := range ancestors {
		switch node.(type) {
		case *ast.GoStmt, *ast.DeferStmt:
			return false
		}
	}
	return true
}
func finalTransportCleanupField(path, file, name string, field *ast.SelectorExpr, ancestors []ast.Node) bool {
	if path != managed || file != "application_controller_ci.go" || name != "StartCIController" || sourceText(field) != "g.finalTransportMu" {
		return false
	}
	for _, node := range ancestors {
		if literal, ok := node.(*ast.FuncLit); ok && captureBodyMatches(&ast.FuncDecl{Body: literal.Body}, recoveryCleanupBody) {
			return true
		}
	}
	return false
}
func workerShutdownBinding(fn *ast.FuncDecl, call *ast.CallExpr, info *types.Info) bool {
	_, observe, valid := signalLifetimeBinding(fn, info)
	return valid && call != nil && call == observe
}
