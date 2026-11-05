package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Comparación exacta de Project con el oráculo independiente de la proyección
// (plan 005 §13; scripts/projection_oracle.py, congelado, no se edita aquí). El
// oráculo lee solo la especificación y devuelve la salida esperada completa; la
// comparación es una secuencia de tokens tipados con los miembros en orden
// (string decodificado, número por su lexema, booleano, null). Un duplicado, una
// constante que no es JSON o basura al final es un error de los dos lados.

// onode es un nodo del árbol ordenado: la misma información que la secuencia de
// tokens.
type onode struct {
	kind byte // 'o' objeto, 'a' arreglo, 's' string, 'n' número, 'b' booleano, 'z' null
	s    string
	b    bool
	keys []string // solo en un objeto, alineadas con vals
	vals []*onode
}

// parseOrdered lee un único valor JSON conservando el orden de las claves y el
// lexema de los números.
func parseOrdered(b []byte) (*onode, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	n, err := readNode(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after the value")
	}
	return n, nil
}

func readNode(d *json.Decoder) (*onode, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := tok.(type) {
	case json.Delim:
		if x == '{' {
			n := &onode{kind: 'o'}
			seen := map[string]bool{}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				if seen[k] {
					return nil, fmt.Errorf("duplicate key %q", k)
				}
				seen[k] = true
				v, err := readNode(d)
				if err != nil {
					return nil, err
				}
				n.keys = append(n.keys, k)
				n.vals = append(n.vals, v)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return n, nil
		}
		n := &onode{kind: 'a'}
		for d.More() {
			v, err := readNode(d)
			if err != nil {
				return nil, err
			}
			n.vals = append(n.vals, v)
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		return n, nil
	case string:
		return &onode{kind: 's', s: x}, nil
	case json.Number:
		return &onode{kind: 'n', s: x.String()}, nil
	case bool:
		return &onode{kind: 'b', b: x}, nil
	case nil:
		return &onode{kind: 'z'}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

// bytes serializa compacto, sin escapar HTML.
func (n *onode) bytes() []byte {
	var b bytes.Buffer
	n.write(&b)
	return b.Bytes()
}

func (n *onode) write(b *bytes.Buffer) {
	switch n.kind {
	case 'o':
		b.WriteByte('{')
		for i, k := range n.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeStr(b, k)
			b.WriteByte(':')
			n.vals[i].write(b)
		}
		b.WriteByte('}')
	case 'a':
		b.WriteByte('[')
		for i, v := range n.vals {
			if i > 0 {
				b.WriteByte(',')
			}
			v.write(b)
		}
		b.WriteByte(']')
	case 's':
		writeStr(b, n.s)
	case 'n':
		b.WriteString(n.s)
	case 'b':
		b.WriteString(strconv.FormatBool(n.b))
	default:
		b.WriteString("null")
	}
}

func writeStr(b *bytes.Buffer, s string) {
	e := json.NewEncoder(b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	b.Truncate(b.Len() - 1) // el salto de línea de Encode
}

// get devuelve el miembro k de un objeto, o nil.
func (n *onode) get(k string) *onode {
	if n == nil || n.kind != 'o' {
		return nil
	}
	for i, kk := range n.keys {
		if kk == k {
			return n.vals[i]
		}
	}
	return nil
}

// del quita el miembro k de un objeto; false si no está.
func (n *onode) del(k string) bool {
	for i, kk := range n.keys {
		if kk == k {
			n.keys = append(n.keys[:i], n.keys[i+1:]...)
			n.vals = append(n.vals[:i], n.vals[i+1:]...)
			return true
		}
	}
	return false
}

func (n *onode) describe() string {
	switch n.kind {
	case 'o':
		return fmt.Sprintf("object of %d members", len(n.keys))
	case 'a':
		return fmt.Sprintf("array of %d", len(n.vals))
	case 's':
		return fmt.Sprintf("string %.60q", n.s)
	case 'n':
		return "number " + n.s
	case 'b':
		return fmt.Sprintf("boolean %v", n.b)
	}
	return "null"
}

// firstDiff devuelve la ruta y el motivo del primer nodo distinto entre lo que
// salió de Project (got) y lo esperado (want), o "" si son iguales. Las rutas
// llevan la forma $.name[0].given[1].
func firstDiff(path string, got, want *onode) string {
	if got.kind != want.kind {
		return fmt.Sprintf("%s: Project has %s, the oracle %s", path, got.describe(), want.describe())
	}
	switch got.kind {
	case 'o':
		for i := 0; i < len(got.keys) && i < len(want.keys); i++ {
			if got.keys[i] != want.keys[i] {
				return fmt.Sprintf("%s: member #%d is %q in Project and %q in the oracle", path, i, got.keys[i], want.keys[i])
			}
			if d := firstDiff(path+"."+got.keys[i], got.vals[i], want.vals[i]); d != "" {
				return d
			}
		}
		if len(got.keys) != len(want.keys) {
			if len(got.keys) > len(want.keys) {
				return fmt.Sprintf("%s: Project has the extra member %q", path, got.keys[len(want.keys)])
			}
			return fmt.Sprintf("%s: the oracle has the extra member %q", path, want.keys[len(got.keys)])
		}
	case 'a':
		for i := 0; i < len(got.vals) && i < len(want.vals); i++ {
			if d := firstDiff(fmt.Sprintf("%s[%d]", path, i), got.vals[i], want.vals[i]); d != "" {
				return d
			}
		}
		if len(got.vals) != len(want.vals) {
			return fmt.Sprintf("%s: %d elements in Project, %d in the oracle", path, len(got.vals), len(want.vals))
		}
	case 's', 'n':
		if got.s != want.s {
			return fmt.Sprintf("%s: Project has %s, the oracle %s", path, got.describe(), want.describe())
		}
	case 'b':
		if got.b != want.b {
			return fmt.Sprintf("%s: Project has %s, the oracle %s", path, got.describe(), want.describe())
		}
	}
	return ""
}

// compareOutputs compara dos textos JSON (Project y el oráculo): "" si son
// iguales, el primer desacuerdo con su ruta si no.
func compareOutputs(got, want []byte) string {
	g, err := parseOrdered(got)
	if err != nil {
		return "$: Project output is not valid JSON: " + err.Error()
	}
	w, err := parseOrdered(want)
	if err != nil {
		return "$: oracle output is not valid JSON: " + err.Error()
	}
	return firstDiff("$", g, w)
}

// ---- El proceso del oráculo ----

// oracleProtocolMarker encabeza todo fallo del protocolo con el oráculo (no
// arranca, se cae, responde basura, sale con error). scripts/mutations.py lo busca:
// una mutación del oráculo que lo provoca es un error de la mutación, nunca una
// detección (code review o2, hallazgo 2).
const oracleProtocolMarker = "ORACLE-PROTOCOL-FAILURE"

type oracleReq struct {
	ID       string   `json:"id"`
	Mode     string   `json:"mode"`
	Elements []string `json:"elements"`
	Resource string   `json:"resource"`
}

type oracleResp struct {
	ID     string `json:"id"`
	Output string `json:"output"`
	Error  string `json:"error"`
}

// parseEnvelope valida la respuesta del oráculo: el `id` es el de la petición (las
// respuestas van en orden), exactamente uno de `output` y `error` está presente y
// `output` es un único valor JSON. Cualquier otra cosa es un fallo del protocolo,
// no un veredicto (code review o3).
func parseEnvelope(line []byte, wantID string) (oracleResp, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return oracleResp{}, fmt.Errorf("unreadable oracle line: %v", err)
	}
	var r oracleResp
	for k, v := range raw {
		switch k {
		case "id", "output", "error":
			var str string
			if err := json.Unmarshal(v, &str); err != nil {
				return oracleResp{}, fmt.Errorf("oracle field %q is not a string", k)
			}
			switch k {
			case "id":
				r.ID = str
			case "output":
				r.Output = str
			default:
				r.Error = str
			}
		default:
			return oracleResp{}, fmt.Errorf("unexpected oracle field %q", k)
		}
	}
	if _, ok := raw["id"]; !ok || r.ID != wantID {
		return oracleResp{}, fmt.Errorf("oracle answered id %q, want %q", r.ID, wantID)
	}
	_, hasOut := raw["output"]
	_, hasErr := raw["error"]
	if hasOut == hasErr {
		return oracleResp{}, fmt.Errorf("oracle reply must carry exactly one of output and error")
	}
	if hasOut {
		dec := json.NewDecoder(strings.NewReader(r.Output))
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil || dec.More() {
			return oracleResp{}, fmt.Errorf("oracle output is not a single JSON value")
		}
	}
	return r, nil
}

// oracleProc es un proceso del oráculo por versión: lee líneas por stdin y
// responde una por cada una, en orden.
type oracleProc struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	errBuf *bytes.Buffer
	report string
	mu     sync.Mutex
}

func oracleScript() string { return filepath.Join("..", "scripts", "projection_oracle.py") }

func startOracle(t testing.TB, v *version) *oracleProc {
	t.Helper()
	report := filepath.Join(t.TempDir(), "rules-"+v.name+".tsv")
	cmd := exec.Command("python3", "-I", "-u", oracleScript(), "--lock", filepath.Join("..", "testdata", v.lock),
		"--packages", packagesDir(t), "--report", report)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	o := &oracleProc{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20), errBuf: &bytes.Buffer{}, report: report}
	cmd.Stderr = o.errBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: cannot start the oracle: %v", oracleProtocolMarker, err)
	}
	return o
}

// ask manda las líneas y espera todas las respuestas. La escritura va en otra
// goroutine para que un recurso grande no bloquee las dos tuberías; work corre
// mientras el oráculo piensa (la salida de Project).
func (o *oracleProc) ask(reqs []oracleReq, work func()) ([]oracleResp, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	werr := make(chan error, 1)
	go func() {
		for _, r := range reqs {
			b, _ := json.Marshal(r)
			if _, err := o.in.Write(append(b, '\n')); err != nil {
				werr <- err
				return
			}
		}
		werr <- nil
	}()
	if work != nil {
		work()
	}
	resps := make([]oracleResp, 0, len(reqs))
	for range reqs {
		line, err := o.out.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("%s: oracle stopped: %v; stderr: %s", oracleProtocolMarker, err, o.errBuf.String())
		}
		r, err := parseEnvelope(line, reqs[len(resps)].ID)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", oracleProtocolMarker, err)
		}
		resps = append(resps, r)
	}
	if err := <-werr; err != nil {
		return nil, fmt.Errorf("%s: writing to the oracle: %v", oracleProtocolMarker, err)
	}
	return resps, nil
}

// close cierra la entrada, espera al proceso y devuelve el informe de reglas.
func (o *oracleProc) close() (string, error) {
	_ = o.in.Close()
	if err := o.cmd.Wait(); err != nil {
		return "", fmt.Errorf("%s: oracle exit: %v; stderr: %s", oracleProtocolMarker, err, o.errBuf.String())
	}
	b, err := os.ReadFile(o.report)
	return string(b), err
}

var (
	sharedMu      sync.Mutex
	sharedOracles = map[string]*oracleProc{}
)

// oracleFor devuelve un proceso del oráculo compartido por versión, para los
// controles positivos (mueren con el binario de test al cerrarse su entrada).
func oracleFor(t testing.TB, v *version) *oracleProc {
	t.Helper()
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if o, ok := sharedOracles[v.name]; ok {
		return o
	}
	o := startOracle(t, v)
	sharedOracles[v.name] = o
	return o
}

// oracleOne manda una sola entrada al oráculo compartido de la versión.
func oracleOne(t testing.TB, v *version, req oracleReq) []byte {
	t.Helper()
	rs, err := oracleFor(t, v).ask([]oracleReq{req}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Error != "" {
		// Los controles positivos son entradas que el oráculo debe poder juzgar.
		t.Fatalf("%s: the oracle cannot judge the input: %s", oracleProtocolMarker, rs[0].Error)
	}
	return []byte(rs[0].Output)
}
