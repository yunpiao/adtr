package dbadmissioncheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
)

const queuedParkSource = "queued_park_ci.go"

var queuedParkBodies = map[string]struct{ file, body string }{
	"lifecycleGate.prepareQueuedSelection": {"queued_park_ci.go", `{
	if budgetMillis <= 0 || budgetMillis > 2000 {
		return errSealProtocol
	}
	deadline := time.Now().Add(time.Duration(budgetMillis) * time.Millisecond)
	if err := g.prepareActivitySelection(kind); err != nil {
		return err
	}
	g.mu.Lock()
	if g.queuedState != "" || g.invalid || g.workerContext == nil || g.workerContext.Err() != nil {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		g.unlockAndPublishInvalid()
		return errSealProtocol
	}
	g.queuedState = "parking"
	g.queuedParked = true
	g.notifyLocked()
	for {
		if g.invalid || g.queuedState != "parking" || g.workerContext.Err() != nil || !time.Now().Before(deadline) {
			g.invalidateBecauseLocked(invalidExplicit)
			g.queuedState = "failed"
			g.queuedParked = false
			g.notifyLocked()
			g.unlockAndPublishInvalid()
			return errSealProtocol
		}
		if g.finalDrainedLocked() {
			g.queuedState = "drained"
			g.mu.Unlock()
			return nil
		}
		changed := g.changed
		remaining := time.Until(deadline)
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
	"lifecycleGate.completeQueuedSelection": {"queued_park_ci.go", `{
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if g.queuedState != "configured" || !g.queuedParked || g.selection != 2 || g.invalid || !g.finalDrainedLocked() {
		g.invalidateBecauseLocked(invalidExplicit)
		g.notifyLocked()
		return errSealProtocol
	}
	g.queuedState = "complete"
	g.queuedParked = false
	g.notifyLocked()
	return nil
}`},
	"lifecycleGate.abortQueuedSelection": {"queued_park_ci.go", `{
	g.mu.Lock()
	defer g.unlockAndPublishInvalid()
	if !g.queuedParked {
		return
	}
	g.invalidateBecauseLocked(invalidExplicit)
	g.queuedState = "failed"
	g.queuedParked = false
	g.notifyLocked()
}`},
	"lifecycleGate.configureActivitySelection": {"activity_selection_ci.go", `{
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.application || g.selection != 1 || g.invalid || g.closing || g.seal != nil || !selectionID(taskID) || !selectionWorkerID(workerID) {
		return errSealProtocol
	}
	if g.queuedState != "" && (g.queuedState != "drained" || !g.queuedParked || !g.finalDrainedLocked()) {
		return errSealProtocol
	}
	key := activityRowKey(g.nonce, taskID)
	if key == workerID {
		return errSealProtocol
	}
	g.selectionTask, g.selectionOwner, g.selectionKey = taskID, workerID, key
	g.selection = 2
	if g.queuedParked {
		g.queuedState = "configured"
	}
	g.notifyLocked()
	return nil
}`},
}

func exactQueuedParkFunction(path, file, name string, fn *ast.FuncDecl) bool {
	expected, ok := queuedParkBodies[name]
	return ok && path == managed && file == expected.file && captureBodyMatches(fn, expected.body)
}
func queuedParkDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
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
			exact := exactQueuedParkFunction(path, filename, name, fn)
			if expected, ok := queuedParkBodies[name]; ok && path == managed && expected.file == queuedParkSource && !exact {
				emit(decl, "queued parking must retain exact bounded physical drain and ACK completion bodies")
			}
			var ancestors []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				switch x := n.(type) {
				case *ast.SelectorExpr:
					selection := info.Selections[x]
					if selection != nil && selection.Kind() == types.FieldVal && isNamed(selection.Recv(), managed, "lifecycleGate") && (selection.Obj().Name() == "queuedState" || selection.Obj().Name() == "queuedParked") && !exact && !exactTransportInvalidation(path, filename, name, fn) && !(name == "lifecycleGate.acquire" && exactRecoveryFunction(path, filename, name, fn)) {
						emit(x, "queued parking state outside its fixed prepare/configure/ACK/acquire path")
					}
				case *ast.CompositeLit:
					if isNamed(info.TypeOf(x), managed, "lifecycleGate") {
						for _, element := range x.Elts {
							if kv, ok := element.(*ast.KeyValueExpr); ok {
								if key, ok := kv.Key.(*ast.Ident); ok && (key.Name == "queuedState" || key.Name == "queuedParked") {
									emit(kv, "queued parking cannot be preinitialized")
								}
							}
						}
					}
				case *ast.Ident:
					obj, ok := info.Uses[x].(*types.Func)
					if !ok || obj.Pkg() == nil || obj.Pkg().Path() != managed {
						break
					}
					signature := callableSignature(obj.Type())
					if signature == nil || signature.Recv() == nil || !isNamed(signature.Recv().Type(), managed, "lifecycleGate") {
						break
					}
					allowed := false
					protected := true
					switch obj.Name() {
					case "prepareQueuedSelection", "completeQueuedSelection":
						allowed = exactFinalQuiesceFunction(path, filename, name, fn) && name == "serveApplicationNormal" && directCallFor(x, ancestors) != nil
					case "abortQueuedSelection":
						allowed = exactFinalQuiesceFunction(path, filename, name, fn) && name == "serveApplicationRescue" && directCallFor(x, ancestors) != nil
					default:
						protected = false
					}
					if protected && !allowed {
						emit(x, "queued parking operation outside its exact synchronous controller caller")
					}
				}
				return true
			})
		}
	}
	return out
}
