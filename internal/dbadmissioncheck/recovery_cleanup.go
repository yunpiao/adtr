package dbadmissioncheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
)

// These complete reviewed bodies bind recovery qualification, native channel
// provenance and every pending/completed transition. They are not a dataflow proof.
var recoveryBodies = map[string]struct{ file, body string }{
	"lifecycleTicket.contaminateRecoveryLocked": {"recovery_cleanup_ci.go", `{
	t.recoveryContaminated = true
	if t.state == 'q' || t.state == 'r' || t.gate.recoveryFinalized {
		t.gate.invalidateBecauseLocked(invalidUncleanClose)
		t.gate.notifyLocked()
	}
}`},
	"lifecycleTicket.canAwaitRecoveryLocked": {"recovery_cleanup_ci.go", `{
	g := t.gate
	if g.invalid || g.recoveryFinalized || g.seal == nil || !g.seal.released || !g.seal.recoveryAuthorized ||
		!t.recoveryAuthorized || t.recoveryEpoch != g.seal.epoch || g.seal.target == t || t.registration != nil || t.recoveryEverRegistered ||
		t.recoveryContaminated || t.operationInFlight != 0 || t.conn == nil ||
		!observation.alreadyClosed || observation.closeFailed || g.recoveryQualified == ^uint64(0) {
		return false
	}
	first := t.firstOperationError
	return first != nil && first == t.firstNativeClosedError && first.Operation == "statement" &&
		first.ContextState == "canceled" && first.ErrorCategory == "canceled" && first.ErrorMatchesContext &&
		first.NativeClosedAtEnd && first.ConnectionMatched && first.RootContextState == "canceled" &&
		first.StartPhase == "released" && first.EndPhase == "released" && diagnosticContextState(g.workerContext) == "canceled"
}`},
	"lifecycleGate.finishRecoveryCleanup": {"recovery_cleanup_ci.go", `{
	deadlineAt := time.Now().Add(2 * time.Second)
	deadline := time.NewTimer(time.Until(deadlineAt))
	defer deadline.Stop()
	g.mu.Lock()
	pending := make([]*lifecycleTicket, 0)
	for _, t := range g.tickets {
		if t.state == 'q' {
			pending = append(pending, t)
		}
	}
	g.mu.Unlock()
	timedOut := false
	for _, t := range pending {
		if !time.Now().Before(deadlineAt) {
			timedOut = true
		}
		if !timedOut {
			select {
			case <-t.cleanupDone:
			case <-deadline.C:
				timedOut = true
			}
		}
		if !time.Now().Before(deadlineAt) {
			timedOut = true
		}
		g.mu.Lock()
		complete := false
		if !timedOut && t.cleanupDone != nil {
			select {
			case <-t.cleanupDone:
				complete = true
			default:
			}
		}
		if !complete || g.tickets[t.generation] != t || t.state != 'q' || t.recoveryContaminated || t.operationInFlight != 0 ||
			t.conn == nil || !t.conn.raw.IsClosed() || g.invalid {
			g.invalidateBecauseLocked(invalidUncleanClose)
		} else {
			t.state = 'r'
			g.recoveryCompleted++
		}
		g.notifyLocked()
		g.mu.Unlock()
	}
	g.mu.Lock()
	// No new qualification or callbacks may change the final success snapshot.
	g.recoveryFinalized = true
	if g.pending != 0 || g.open != 0 {
		g.invalidateBecauseLocked(invalidCloseState)
	}
	for _, t := range g.tickets {
		if t.state == 'p' || t.state == 'o' || t.state == 'x' || t.state == 'q' || t.operationInFlight != 0 {
			g.invalidateBecauseLocked(invalidCloseState)
		}
	}
	if g.recoveryCompleted != g.recoveryQualified {
		g.invalidateBecauseLocked(invalidUncleanClose)
	}
	g.notifyLocked()
	g.mu.Unlock()
}`},
	"lifecycleGate.writeRecoveryCleanupDiagnostic": {"recovery_cleanup_ci.go", `{
	g.mu.Lock()
	d := recoveryCleanupDiagnostic{Event: "owned_ci_recovery_cleanup", Qualified: g.recoveryQualified, Completed: g.recoveryCompleted, Open: g.open, ConnectPending: g.pending}
	if g.seal != nil {
		d.CaptureAuthorized = g.seal.recoveryAuthorized
		d.Released = g.seal.released
	}
	active := false
	for _, t := range g.tickets {
		if t.state == 'q' {
			d.Pending++
		}
		if t.operationInFlight != 0 {
			active = true
		}
	}
	if g.invalid {
		d.Failed = 1
	}
	d.Settled = g.recoveryFinalized && !active && d.Pending == 0 && d.Open == 0 && d.ConnectPending == 0 && d.Qualified == d.Completed && d.Failed == 0
	g.mu.Unlock()
	data, err := json.Marshal(d)
	if err == nil {
		_, _ = os.Stderr.Write(append(data, '\n'))
	}
}`},
	"lifecycleGate.registerActivityRow": {"seal_ci.go", `{
	g.mu.Lock()
	ticket, ok := conn.permit.(*lifecycleTicket)
	if !ok || ticket.gate != g || ticket.conn != conn || ticket.state != 'o' || ticket.backend == 0 || ticket.registration != nil || g.invalid || g.closing {
		g.mu.Unlock()
		return nil, "", errSealProtocol
	}
	if g.next == ^uint64(0) {
		g.invalidateBecauseLocked(invalidCapacity)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return nil, "", errSealProtocol
	}
	key := workerID
	if g.application && g.selection == 1 {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return nil, "", errSealProtocol
	}
	if g.application && g.selection == 2 && identity.TaskID == g.selectionTask {
		if identity.Kind != g.selectionKind || workerID != g.selectionOwner || g.selectionConsumed {
			g.invalidateBecauseLocked(invalidExplicit)
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return nil, "", errSealProtocol
		}
		key = g.selectionKey
		g.selectionConsumed = true
	}
	g.next++
	registration := &activityRegistration{identity: identity, backend: ticket.backend, generation: g.next, activityKey: key}
	ticket.recoveryEverRegistered = true
	ticket.registration = registration
	g.notifyLocked()
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		defer g.unlockAndPublishInvalid()
		if ticket.registration != registration {
			return
		}
		if g.seal != nil && !g.seal.released && g.seal.registration == registration && !g.captureObligationsEndedLocked() {
			g.invalidateBecauseLocked(invalidTargetRevoked)
		}
		ticket.registration = nil
		g.notifyLocked()
	}, key, nil
}`},
	"lifecycleGate.release": {"seal_ci.go", `{
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seal == nil || epoch != g.seal.epoch {
		return errSealProtocol
	}
	if g.seal.released {
		return nil
	}
	// Consume only before release; rescue remains possible without permission.
	g.seal.recoveryAuthorized = g.captureObligationsEndedLocked() && !g.invalid
	g.seal.released = true
	g.closing = false
	g.notifyLocked()
	return nil
}`},
	"lifecycleGate.acquire": {"lifecycle_ci.go", `{
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			publish := g.invalidateWaitLocked()
			g.mu.Unlock()
			if publish != nil {
				publish()
			}
			return nil, err
		}
		released := g.seal != nil && g.seal.released
		if (g.invalid && !released && g.seal == nil) || (g.closing && g.seal == nil) {
			g.mu.Unlock()
			return nil, errAdmissionClosed
		}
		if g.closing || g.finalParked {
			if g.changed == nil {
				g.changed = make(chan struct{})
			}
			changed := g.changed
			g.mu.Unlock()
			select {
			case <-changed:
			case <-ctx.Done():
			}
			continue
		}
		if (g.witness != nil && len(g.tickets) >= 4096) || g.next == ^uint64(0) || g.total == ^uint64(0) {
			g.invalidateBecauseLocked(invalidCapacity)
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return nil, errAdmissionClosed
		}
		g.total++
		g.next++
		if g.tickets == nil {
			g.tickets = make(map[uint64]*lifecycleTicket)
		}
		ticket := &lifecycleTicket{gate: g, generation: g.next, state: 'p', birthPhase: g.diagnosticPhaseLocked(), birthCaptureEnded: g.seal != nil && g.seal.captureEnded}
		if g.seal != nil && g.seal.released && g.seal.recoveryAuthorized && !g.invalid && !g.recoveryFinalized {
			ticket.recoveryEpoch = g.seal.epoch
			ticket.recoveryAuthorized = true
		}
		g.tickets[ticket.generation] = ticket
		g.pending++
		g.notifyLocked()
		g.mu.Unlock()
		return ticket, nil
	}
}`},
	"lifecycleGate.locallyDrained": {"lifecycle_ci.go", `{
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closing || g.invalid || g.pending != 0 || g.open != 0 {
		return false
	}
	for _, t := range g.tickets {
		if t.state == 'p' || t.state == 'o' || t.state == 'x' || t.state == 'q' || t.operationInFlight != 0 {
			return false
		}
	}
	return true
}`},
	"lifecycleTicket.closedObserved": {"lifecycle_ci.go", `{
	clean := !observation.alreadyClosed && !observation.closeFailed && observation.cleanupDone
	g := t.gate
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if t.state != 'o' && t.state != 'x' {
		g.invalidateBecauseLocked(invalidCloseState)
		g.notifyLocked()
		return
	}
	// Inspect registration before clearing it. A closed candidate or any epoch
	// target remains retained; its release callback/ACK may still refer to it.
	if t.operationInFlight != 0 || g.recoveryFinalized {
		g.invalidateBecauseLocked(invalidCloseState)
	}
	if !clean && t.canAwaitRecoveryLocked(observation) {
		t.state = 'q'
		t.cleanupDone = t.conn.raw.PgConn().CleanupDone()
		g.open--
		g.recoveryQualified++
		g.notifyLocked()
		return
	}
	prunable := clean && !g.invalid && t.registration == nil &&
		(g.seal == nil || g.seal.target != t)
	if !clean {
		first := g.firstInvalidReason == ""
		g.invalidateBecauseLocked(invalidUncleanClose)
		if first {
			role := "other"
			if g.seal != nil && g.seal.target == t {
				role = "epoch_target"
			}
			g.firstUncleanClose = &uncleanCloseDiagnostic{"owned_ci_first_unclean_close", observation.alreadyClosed, observation.closeFailed, observation.cleanupDone, role}
			g.firstUncleanOperation = &uncleanOperationDiagnostic{"owned_ci_unclean_operation", t.birthPhase, t.birthCaptureEnded, t.firstOperationError, t.firstNativeClosedError, diagnosticContextState(g.workerContext)}
		}
	} else if g.seal != nil && !g.seal.released && g.seal.target == t && !g.captureObligationsEndedLocked() {
		g.invalidateBecauseLocked(invalidTargetClosed)
	}
	g.open--
	t.state = 'c'
	t.registration = nil
	if prunable {
		// No invalid publisher exists while !g.invalid. The mutex serializes
		// closure with pin, registration and complete snapshots. Preserve the
		// ticket's unique generation/state for any stale local handle.
		delete(g.tickets, t.generation)
		t.conn = nil
		t.backend = 0
	}
	g.notifyLocked()
}`},
}

func exactRecoveryFunction(path, file, name string, fn *ast.FuncDecl) bool {
	expected, ok := recoveryBodies[name]
	return ok && path == managed && file == expected.file && captureBodyMatches(fn, expected.body)
}

const recoveryCleanupBody = `{
 g.finalTransportMu.Lock()
 stopOnce.Do(func(){stopping.Store(true);normal.Close();rescue.Close()})
 g.finalTransportMu.Unlock()
 wg.Wait()
 recoveryOnce.Do(func(){g.finishRecoveryCleanup()})
 g.mu.Lock()
 g.captureEndFD = 0
 captureEnd.Close()
 g.mu.Unlock()
 reportOnce.Do(func(){g.writeFinalInvalidationDiagnostic();g.writeRecoveryCleanupDiagnostic()})
}`

func recoveryCleanupCaller(path, file, name string, id *ast.Ident, ancestors []ast.Node, info *types.Info) bool {
	if path != managed || file != "application_controller_ci.go" || name != "StartCIController" || !captureConsumerCall(id, ancestors, true) {
		return false
	}
	for i, node := range ancestors {
		literal, ok := node.(*ast.FuncLit)
		if !ok || i == 0 || !captureBodyMatches(&ast.FuncDecl{Body: literal.Body}, recoveryCleanupBody) {
			continue
		}
		assignment, ok := ancestors[i-1].(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || sourceText(assignment.Lhs[0]) != "cleanup" || len(assignment.Rhs) != 1 || assignment.Rhs[0] != literal {
			return false
		}
		// The exact cleanup body binds g, both Once calls and the controller join;
		// type-check the used receiver rather than trust a same-spelled object.
		selector, ok := ancestors[len(ancestors)-2].(*ast.SelectorExpr)
		if !ok || sourceText(selector.X) != "g" {
			return false
		}
		selection := info.Selections[selector]
		onceCount, joinCount := 0, 0
		ast.Inspect(literal.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			member := info.Selections[method]
			if member != nil && member.Kind() == types.MethodVal {
				if (sourceText(method) == "stopOnce.Do" || sourceText(method) == "recoveryOnce.Do" || sourceText(method) == "reportOnce.Do") && isNamed(member.Recv(), "sync", "Once") {
					onceCount++
				}
				if sourceText(method) == "wg.Wait" && isNamed(member.Recv(), "sync", "WaitGroup") {
					joinCount++
				}
			}
			return true
		})
		return onceCount == 3 && joinCount == 1 && selection != nil && selection.Kind() == types.MethodVal && isNamed(selection.Recv(), managed, "lifecycleGate")
	}
	return false
}
func recoveryNativeRead(path, file, name string, fn *ast.FuncDecl, sel *ast.SelectorExpr, info *types.Info) bool {
	if !exactRecoveryFunction(path, file, name, fn) {
		return false
	}
	selection := info.Selections[sel]
	if selection == nil || selection.Kind() != types.MethodVal {
		return false
	}
	switch name {
	case "lifecycleTicket.closedObserved":
		return (sourceText(sel) == "t.conn.raw.PgConn" && isNamed(selection.Recv(), pgx, "Conn")) || (sourceText(sel) == "t.conn.raw.PgConn().CleanupDone" && isNamed(selection.Recv(), pgx+"/pgconn", "PgConn"))
	case "lifecycleGate.finishRecoveryCleanup":
		return sourceText(sel) == "t.conn.raw.IsClosed" && isNamed(selection.Recv(), pgx, "Conn")
	}
	return false
}
func recoveryField(typ types.Type, name string) bool {
	if isNamed(typ, managed, "sealAttempt") {
		return name == "recoveryAuthorized"
	}
	if isNamed(typ, managed, "lifecycleGate") {
		return name == "recoveryQualified" || name == "recoveryCompleted" || name == "recoveryFinalized"
	}
	if isNamed(typ, managed, "lifecycleTicket") {
		switch name {
		case "recoveryEverRegistered", "recoveryEpoch", "recoveryAuthorized", "recoveryContaminated", "operationSequence", "activeOperation", "operationInFlight", "cleanupDone", "firstOperationError", "firstNativeClosedError":
			return true
		}
	}
	return isNamed(typ, managed, "operationErrorDiagnostic") || isNamed(typ, managed, "operationTrace")
}
func recoveryObject(typ types.Type) bool {
	return isNamed(typ, managed, "lifecycleTicket") || isNamed(typ, managed, "lifecycleGate") || isNamed(typ, managed, "sealAttempt") || isNamed(typ, managed, "operationErrorDiagnostic") || isNamed(typ, managed, "operationTrace") || isNamed(typ, managed, "operationTraceKey")
}
func recoveryDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
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
			exact := exactRecoveryFunction(path, filename, name, fn)
			observer := exactObserverFunction(path, filename, name, fn)
			if expected, ok := recoveryBodies[name]; ok && path == managed && (expected.file == "recovery_cleanup_ci.go" || name == "lifecycleGate.locallyDrained" || name == "lifecycleGate.release") && !exact {
				emit(decl, "recovery helpers must retain their complete reviewed qualification, budget and completion bodies")
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
					if path == managed && x.Name.Name == "recoveryCleanupDiagnostic" {
						expected, err := parser.ParseFile(token.NewFileSet(), "expected.go", "package expected;type expected "+recoveryDiagnosticType, 0)
						if filename != "recovery_cleanup_ci.go" || err != nil || compactSource(x.Type) != compactSource(expected.Decls[0].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type) {
							emit(x, "recovery summary must have exactly the reviewed count and boolean fields")
						}
					}
				case *ast.SelectorExpr:
					selection := info.Selections[x]
					if selection != nil && selection.Kind() == types.FieldVal && recoveryField(selection.Recv(), selection.Obj().Name()) && !exact && !observer && !exactFinalQuiesceFunction(path, filename, name, fn) && !(name == "lifecycleTicket.failedObserved" && exactConnectFailureFunction(path, filename, name, fn)) && !uncleanCapture(path, filename, name, ancestors) {
						emit(x, "recovery qualification or observation escapes its fixed producer/consumer")
					}
				case *ast.StarExpr:
					if info.Types[x].IsValue() && recoveryObject(info.TypeOf(x)) {
						emit(x, "whole recovery object copies or replacements bypass monotonic qualification")
					}
				case *ast.CompositeLit:
					if (isNamed(info.TypeOf(x), managed, "operationTrace") || isNamed(info.TypeOf(x), managed, "operationTraceKey")) && !observer {
						emit(x, "trace identity and sequence can only originate in the exact observer")
					}
					if recoveryObject(info.TypeOf(x)) {
						for _, element := range x.Elts {
							kv, ok := element.(*ast.KeyValueExpr)
							if !ok && !observer {
								emit(element, "positional recovery object construction is forbidden")
								continue
							}
							if ok {
								key, ok := kv.Key.(*ast.Ident)
								if ok && (recoveryField(info.TypeOf(x), key.Name) || (key.Name == "state" && recoveryPendingState(kv.Value, info))) && !exact && !observer {
									emit(kv, "recovery fields cannot be preauthorized in a literal")
								}
							}
						}
					}
				case *ast.AssignStmt:
					for i, lhs := range x.Lhs {
						if selection, ok := lhs.(*ast.SelectorExpr); ok && i < len(x.Rhs) {
							selected := info.Selections[selection]
							if selected != nil && isNamed(selected.Recv(), managed, "lifecycleTicket") && selected.Obj().Name() == "state" && recoveryPendingState(x.Rhs[i], info) && !exact {
								emit(lhs, "pending recovery transitions require their exact reviewed bodies")
							}
						}
						if info.TypeOf(lhs) != nil && recoveryObject(info.TypeOf(lhs)) && !exact && !observer {
							if _, pointer := info.TypeOf(lhs).(*types.Pointer); !pointer {
								emit(lhs, "whole recovery values cannot be overwritten")
							}
						}
					}
				case *ast.Ident:
					obj, ok := info.Uses[x].(*types.Func)
					if !ok || obj.Pkg() == nil || obj.Pkg().Path() != managed {
						break
					}
					sig := callableSignature(obj.Type())
					if sig == nil || sig.Recv() == nil {
						break
					}
					allowed := false
					protected := true
					switch obj.Name() {
					case "contaminateRecoveryLocked":
						allowed = isNamed(sig.Recv().Type(), managed, "lifecycleTicket") && observer && (name == "operationObserver.TraceQueryStart" || name == "operationObserver.TraceQueryEnd") && directCallFor(x, ancestors) != nil
					case "canAwaitRecoveryLocked":
						allowed = isNamed(sig.Recv().Type(), managed, "lifecycleTicket") && exact && name == "lifecycleTicket.closedObserved" && directCallFor(x, ancestors) != nil
					case "finishRecoveryCleanup":
						allowed = isNamed(sig.Recv().Type(), managed, "lifecycleGate") && recoveryCleanupCaller(path, filename, name, x, ancestors, info)
					case "writeRecoveryCleanupDiagnostic":
						allowed = isNamed(sig.Recv().Type(), managed, "lifecycleGate") && recoveryCleanupCaller(path, filename, name, x, ancestors, info) && allowedFinalDiagnosticCaller(path, filename, name, obj.Name(), x, ancestors, info)
					default:
						protected = false
					}
					if protected && !allowed {
						emit(x, "recovery operation outside its sole synchronous reviewed caller")
					}
				}
				return true
			})
		}
	}
	return out
}

const recoveryDiagnosticType = "struct {\n\tEvent             string `json:\"event\"`\n\tCaptureAuthorized bool   `json:\"captureAuthorized\"`\n\tReleased          bool   `json:\"released\"`\n\tQualified         uint64 `json:\"qualified\"`\n\tCompleted         uint64 `json:\"completed\"`\n\tPending           int    `json:\"pending\"`\n\tOpen              int    `json:\"open\"`\n\tConnectPending    int    `json:\"connectPending\"`\n\tFailed            int    `json:\"failed\"`\n\tSettled           bool   `json:\"settled\"`\n}"

func recoveryPendingState(expr ast.Expr, info *types.Info) bool {
	value := info.Types[expr].Value
	return value != nil && (value.ExactString() == "113" || value.ExactString() == "114")
}
