package dbadmissioncheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
)

const transportInvalidationBody = `{
	g.mu.Lock()
	g.invalidateBecauseLocked(invalidTransport)
	// A failed publication can enter here synchronously from publishInvalid.
	// Release parked admissions without attempting publication on that same
	// broken channel or waiting for our own in-progress publication to finish.
	if g.finalParked {
		g.finalQuiesce.state = "failed"
		g.finalParked = false
	}
	if g.queuedParked {
		g.queuedState = "failed"
		g.queuedParked = false
	}
	g.notifyLocked()
	g.mu.Unlock()
}`
const legacySealTransportBody = `{
	var writeMu sync.Mutex
	var invalidOnce sync.Once
	done := make(chan struct{})
	defer close(done)
	closePipes := func() {
		if c, ok := requests.(io.Closer); ok {
			_ = c.Close()
		}
		if c, ok := replies.(io.Closer); ok {
			_ = c.Close()
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			gate.invalidateTransport()
			closePipes()
		case <-done:
		}
	}()
	send := func(frame sealFrame) error {
		data, err := json.Marshal(frame)
		if err != nil {
			return errSealProtocol
		}
		data = append(data, '\n')
		writeMu.Lock()
		defer writeMu.Unlock()
		n, err := replies.Write(data)
		if err != nil || n != len(data) {
			gate.invalidateTransport()
			closePipes()
			return errSealProtocol
		}
		return nil
	}
	publishInvalid := func() {
		invalidOnce.Do(func() { frame := gate.frame(1); frame.Status = "invalid"; _ = send(frame) })
	}
	reader := bufio.NewReaderSize(requests, 1024)
	for count := 0; count < 128; count++ {
		line, err := reader.ReadSlice('\n')
		if err != nil || len(line) > 512 {
			gate.invalidateTransport()
			publishInvalid()
			return errSealProtocol
		}
		var request sealRequest
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || request.Version != 1 || request.Epoch != 1 || ctx.Err() != nil {
			gate.invalidateTransport()
			publishInvalid()
			return errSealProtocol
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			gate.invalidateTransport()
			publishInvalid()
			return errSealProtocol
		}
		switch request.Op {
		case "status":
			if request.Target != 0 {
				err = errSealProtocol
			}
		case "pin":
			err = gate.pin(request.Epoch, request.Target, publishInvalid)
		case "release":
			if request.Target != 0 {
				err = errSealProtocol
			} else {
				err = gate.release(request.Epoch)
			}
		default:
			err = errSealProtocol
		}
		if err != nil {
			gate.invalidateTransport()
			publishInvalid()
			return errSealProtocol
		}
		if err = send(gate.frame(request.Epoch)); err != nil {
			return err
		}
		if request.Op == "release" {
			return nil
		}
	}
	gate.invalidateTransport()
	publishInvalid()
	return errSealProtocol
}`

func exactTransportInvalidation(path, file, name string, fn *ast.FuncDecl) bool {
	return path == managed && file == "seal_ci.go" && name == "lifecycleGate.invalidateTransport" && captureBodyMatches(fn, transportInvalidationBody)
}
func transportInvalidationDeclarations(fset *token.FileSet, path string, files []*ast.File, info *types.Info) []Diagnostic {
	var out []Diagnostic
	if path != managed {
		return out
	}
	emit := func(n ast.Node, reason string) { out = append(out, Diagnostic{fset.Position(n.Pos()), reason}) }
	for _, file := range files {
		filename := filepath.Base(fset.Position(file.Pos()).Filename)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				name = receiverName(fn.Recv.List[0].Type) + "." + name
			}
			if name == "lifecycleGate.invalidateTransport" && !exactTransportInvalidation(path, filename, name, fn) {
				emit(fn, "transport invalidation must retain sticky-first conditional unpark without publication")
			}
			if name == "recordApplicationFailure" && !exactFinalQuiesceFunction(path, filename, name, fn) {
				emit(fn, "application transport failure must retain its exact non-reentrant cancellation and close path")
			}
			var ancestors []ast.Node
			ast.Inspect(fn, func(n ast.Node) bool {
				if n == nil {
					ancestors = ancestors[:len(ancestors)-1]
					return false
				}
				ancestors = append(ancestors, n)
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				obj, ok := info.Uses[id].(*types.Func)
				if !ok || obj.Pkg() == nil || obj.Pkg().Path() != managed || obj.Name() != "invalidateTransport" {
					return true
				}
				sig := callableSignature(obj.Type())
				if sig == nil || sig.Recv() == nil || !isNamed(sig.Recv().Type(), managed, "lifecycleGate") {
					return true
				}
				allowed := name == "recordApplicationFailure" && exactFinalQuiesceFunction(path, filename, name, fn) && directCallFor(id, ancestors) != nil
				// The isolated legacy helper has seven existing sites, including its bounded
				// cancellation goroutine. Preserve that entire body rather than whitelist a file.
				if !allowed && filename == "seal_protocol_ci.go" && name == "runSealProtocol" && captureBodyMatches(fn, legacySealTransportBody) && len(ancestors) >= 3 {
					selector, ok := ancestors[len(ancestors)-2].(*ast.SelectorExpr)
					if ok && selector.Sel == id {
						call, ok := ancestors[len(ancestors)-3].(*ast.CallExpr)
						allowed = ok && call.Fun == selector && len(call.Args) == 0
					}
				}
				if !allowed {
					emit(id, "transport invalidation outside its exact application failure or isolated legacy helper")
				}
				return true
			})
		}
	}
	return out
}
