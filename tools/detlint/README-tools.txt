detlint gate tools (nested module "detlint", stdlib only)

Build once (on the host, from the repository root; scripts/gate.sh does this itself):
  BIN=$(mktemp -d); (cd tools/detlint && go build -o "$BIN" ./...)
  -> $BIN/detlint $BIN/nocomments $BIN/astdiff $BIN/paniccheck $BIN/importcheck
     $BIN/importlint $BIN/boundlint
Or run one directly: (cd tools/detlint && go run . -root "$OLDPWD" internal/pool internal/sim/...)
                     (cd tools/detlint && go run ./cmd/nocomments -root "$OLDPWD")
Copied from another repository: see SOURCE.md. importlint and boundlint (sections 6 and 7)
were written here.

All tools: findings on stdout, one per line, sorted: path:line:col: <rule-id>: message
Exit: 0 clean, 1 findings, 2 usage/internal error (message on stderr).
Paths in findings are relative to -root (or to the trees, for astdiff).

1. detlint [-tests] [-allow FILE] [-allow-import P1,P2,...] -root DIR PKGDIR...
   PKGDIR relative to DIR; "dir/..." recurses (skips testdata, vendor, .x, _x, nested modules).
   Missing dirs are skipped silently. Summary on stderr: "detlint: checked N files in M packages".
   Default: non-test files only; -tests adds _test.go (in-package and external _test packages).
   Type-checks with go/types (root module from source, stdlib + module deps via the source
   importer). Type errors -> [typecheck], parse/read errors -> [parse].
   In a checked directory NO .go file is dropped: every non-test .go file (and with -tests
   every _test.go file) is type-checked and linted whatever its build constraints or name.
   Module packages that a checked package imports but that are not themselves checked are
   loaded as the default build sees them (build.Default.MatchFile, cgo off).
   One finding per (file, line, rule).

   Rules:
   [import]   A non-test file may import only packages of the root module (path == module
              path from DIR/go.mod, or that path + "/...") and these standard packages:
              bufio bytes cmp container/heap container/list container/ring crypto/sha256
              crypto/sha512 crypto/subtle embed encoding/binary encoding/hex encoding/base64
              errors fmt hash hash/crc32 hash/crc64 hash/fnv io iter maps math math/bits
              math/rand/v2 slices sort strconv strings time unicode unicode/utf8 unicode/utf16
              plus whatever -allow-import adds. Anything else is a finding at the import spec,
              used or not (blank and dot imports included). _test.go files (with -tests) are
              not subject to the list; there an import of crypto/rand -> [cryptorand],
              hash/maphash or unsafe -> [procstate], net, net/http..., os/exec, os/signal,
              syscall..., plugin -> [realio].
   [buildtag] In each checked directory (test files included, -tests or not): a .go file with
              a //go:build or // +build line (any parsed comment; for test files without
              -tests only the header up to the imports is parsed); a .go file whose name has a
              GOOS/GOARCH suffix (_linux.go, _amd64.go, _windows_arm64_test.go, ... decided by
              go/build's own MatchFile with an unknown GOOS/GOARCH); a .go file importing "C";
              any other .go file build.Default.MatchFile would exclude; a file the go tool
              would compile or link (.c .cc .cxx .cpp .m .h .hh .hpp .hxx .f .F .for .f90 .s .S
              .sx .swig .swigcxx .syso); a .go or such source file whose name starts with "_" or
              "." ("file is invisible to the go tool"). Position 1:1 except constraint lines
              and import "C" specs.
   [wallclock]  uses of time.Now Since Until Sleep After AfterFunc Tick NewTimer NewTicker
              (calls, values, aliased and dot imports); context.WithTimeout/WithDeadline(+Cause).
   [globalrand] package-level functions of math/rand/v2 and math/rand other than the
              constructors New NewPCG NewChaCha8 NewSource NewZipf.
   [concurrency] go, select, channel types/variables/send/receive/range/close, any use of
              sync or sync/atomic.
   [procstate] os.Getpid Getppid Hostname Getenv LookupEnv Environ Args Getwd; runtime
              NumGoroutine NumCPU GOMAXPROCS Stack Caller Callers; reflect.Value.Pointer,
              UnsafePointer, UnsafeAddr.   [realio] any other use of package os.
              (os, sync, runtime, reflect, context are not importable in non-test files, so in
              those these use rules fire next to the [import] finding.)
   [ptrfmt]   (a) In a call of any function or method of package fmt (resolved with go/types)
              except the Scan family, an argument bound to an `any`/`...any` parameter whose
              STATIC type can print an address: pointer, chan, func, unsafe.Pointer, uintptr,
              or a map/slice/array/struct containing one at any depth (recursive types are
              followed once), or a type parameter with such a type term. The io.Writer of
              Fprint*, the format string and the []byte of Append* are not printed and not
              checked. A multi-value call argument f() is checked per result.
              (b) Any use of a fmt function or method as a value (not the callee of a direct
              call), e.g. f := fmt.Sprintf, since its arguments can then not be checked.
              (c) A %p verb in a constant string argument of any call to a function or method
              whose name ends in "f".
              LIMIT: an argument whose static type is an interface (error, any, fmt.Stringer,
              ...) is not flagged: its dynamic type is unknown. So a wrapper
              `func logf(f string, a ...any) { fmt.Printf(f, a...) }` hides the types its
              callers pass. A type parameter whose constraint has no type terms (any,
              comparable, method-only) is treated the same. Structs with pointer fields are
              flagged even if they implement String() (fmt does not call it for %d, %#v, ...),
              e.g. time.Time (it has a *Location field). Builtin print/println are not checked.
   [maprange] A range over a map (or a type parameter with a map type term) is accepted only
              if (1) its body is empty, or (2) its body consists only of `s = append(s, k)` /
              `s = append(s, k, v)` statements (k, v: the range key/value identifiers, s: one
              slice variable) and the very next statement of the same block (case clause,
              select clause; a labeled range counts as the labeled statement) is exactly
              slices.Sort(s), sort.Strings(s) or sort.Ints(s). Comparator sorts (sort.Sort,
              sort.Slice, sort.SliceStable, slices.SortFunc, slices.SortStableFunc) do not
              qualify: a comparator can leave ties, and ties expose map order -> allowlist it.
              Function literals follow the same rule. maps.Keys/Values/All is accepted only as
              the single direct argument of slices.Sorted(...) (not SortedFunc/
              SortedStableFunc); any other use is a [maprange] finding.
   [float]    any expression, variable or type expression of float/complex type; and a type
              parameter whose constraint's type terms (unions, ~T, embedded constraint
              interfaces at any depth, e.g. cmp.Ordered) include a float or complex type,
              reported at the type parameter. LIMIT: a type parameter constrained by any or
              comparable is not flagged (its instantiation with float64 in a checked package is
              caught at the call site as a float type expression).
   Also: [typecheck] [parse] [stale-allow] [allow-format].

   -allow lines: relative/file.go<TAB>Func<TAB>rule-id<TAB>justification
     Func is Func or Type.Method ((*T).M and T[K].M -> T.M); code outside any func is "-"
     (so [import] and [buildtag] findings use "-"); func literals belong to their enclosing
     func. '#' and blank lines ignored. Empty field/justification -> [allow-format]. Entry
     that suppressed nothing in THIS run -> [stale-allow] (so pass the full package set
     whenever you pass -allow).
   -allow-import: comma-separated import paths added to the [import] allowlist (exact paths,
     no prefixes).
   Example: $BIN/detlint -root . -allow tools/lint-rules/detlint-allow.txt internal/pool internal/sim/...
   (-allow path is relative to the current directory, not to -root.)

2. nocomments -root DIR [-baseline OLD] [PATH...]
   PATHs relative to DIR (dirs or single files); default ".". Skips dirs named
   agent_work .git testdata vendor tools (anywhere below the start path; astdiff and
   paniccheck skip the same names, and importlint reports a directory named agent_work,
   tools or vendor below the top level, so the skip cannot hide product code).
   Allowed: comments matching exactly ^//go:(build|embed|generate|noinline)( |$), where a
   //go:build line must also sit before the package clause; "// Output:" / "// Unordered
   output:" comment groups inside the body of an Example* func in a _test.go file.
   Everything else -> [comment]: other //go: directives (//go:linkname, //go:nosplit,
   //go:embedx, ...), //line, //nolint, "// go:x", /* */, "// Code generated ... DO NOT EDIT."
   (no exception: the project has no generated code). Unparsable file -> [parse].
   -baseline OLD: per file, a non-directive comment is accepted as many times as its exact text
   occurs in the same file (same relative path) under OLD; each further copy is a finding.
   A misplaced //go:build is a finding even if OLD had it.
   Example: $BIN/nocomments -root .

3. astdiff TREE_A TREE_B
   Every .go file (same dir exclusions as nocomments) must exist in both and have identical
   ASTs ignoring positions and comments (formatting-only changes pass). Significant comments
   must match in text, order and placement: //go:* lines, // +build lines, //line and /*line,
   //export, //extern, the cgo preamble (the whole comment group documenting import "C",
   text identical), and Example output blocks in _test.go. Placement is structural: the path
   of child indices from the file down to the innermost node whose source range contains the
   comment, ending in "gap before child i" or "after the last child", so moving //go:embed
   from one ValueSpec to another inside one var ( ... ) block is a difference.
   Also compared byte for byte, and required to exist in both trees: every file named
   go.mod, go.sum, go.work, go.work.sum, or with extension .c .cc .cxx .cpp .m .h .hh .hpp
   .hxx .f .F .for .f90 .s .S .sx .swig .swigcxx .syso. All other non-Go files (.txt, .md,
   embedded data, ...) are ignored.
   Rule [astdiff], one finding per differing file (first difference); added/removed files and
   differing byte-compared files reported at :1:1.
   LIMIT: where a node's children overlap in source range (a method's receiver lies inside
   its FuncType's range), two gaps can share an anchor; no directive is meaningful there.
   Example (pre-prose vs post-prose):
     git worktree add /tmp/pre <sha>; $BIN/astdiff /tmp/pre .
     (or: git archive <sha> | tar -x -C /tmp/pre)

4. paniccheck -root DIR -allow FILE [DIR...]
   DIRs relative to root, default "internal"; same dir exclusions. Every .go file found there,
   whatever its build constraints and including _x.go / .x.go names, is parsed.
   [panic-shadow]: any declaration of an identifier named panic in ANY of those files, test
   files included: func or method name, import name, const/var/type spec, parameter, result,
   receiver, struct field, type parameter, := variable, range or type-switch variable, label.
   [panic]: every call whose callee is the bare identifier panic in a non-test file, not
   covered by an allowlist line. There is no "user-defined panic" escape any more.
   Allowlist lines: relative/file.go<TAB>EnclosingFunc<TAB>justification
   (EnclosingFunc as for detlint; one entry covers every panic in that function.)
   Entry matching no panic -> [stale-allow]; empty justification or wrong field count ->
   [allow-format]; unparsable file -> [parse].
   Example: $BIN/paniccheck -root . -allow tools/lint-rules/panic-allow.txt

5. importcheck -module M [-test-extra P1,P2,...] < file-list
   Reads .go file paths on stdin (one per line) and parses each with parser.ImportsOnly,
   whatever its build tags. Product files may import only the standard library (first path
   element without a dot, "C" excluded) and M/...; _test.go files may also import P/... for
   each -test-extra P. A syntax error after the import block does not hide the imports (they
   are still checked); a file whose package clause or import block does not parse is a
   finding ("cannot parse"). Output lines: path:line:col: import: message.

   Not run by this project's gate (importlint replaces it); kept with its tests.

6. importlint -root DIR -rules FILE
   Enforces the import rules of PLAN.md section 6.3 and the go.mod requirement rule. The
   rules live in tools/lint-rules/imports.txt so a later chunk extends them without a code
   change. Every .go file under DIR is read with parser.ImportsOnly, whatever its build
   tags, test files included; directories named agent_work .git testdata vendor tools
   _site are skipped. A package is the directory a file is in; rules about a package that
   does not exist yet match nothing and are silent.
   Rules file, one directive per line ('#' and blank lines ignored; a file that does not
   parse is exit 2, not a finding):
     module <path>                    required once; must equal the module line of DIR/go.mod
     require <module path>            a module go.mod may require directly (any number of lines)
     deny <pkg>... : <import>...      the packages on the left may not import those on the right
     only <import>... : <pkg>...      the imports on the left may be imported ONLY by the
                                      packages on the right; this is also what permits a
                                      third-party import at all
   Patterns: a pattern whose first path element contains a dot is a full import path
   (github.com/jackc/pgx/v5/pgproto3); any other is a directory of this module relative to
   its root (internal/pool; "." is the root package). A trailing /... also matches
   everything below (internal/pool/... matches internal/pool and internal/pool/x; "..." is
   the whole module). "!pattern" takes packages out of the side it is written on:
   `internal/obs/... !internal/obs` is every package below internal/obs but not internal/obs.
   Findings:
   [deny]          an import a deny rule forbids (the message quotes the rule and its line).
   [only]          an import named by an only rule, in a package the rule does not list.
   [third-party]   an import outside the standard library (first path element has no dot;
                   "C" is not standard) and outside this module that no only rule names.
   [gomod]         the go.mod rule, exactly: (a) the module line equals the rules file's;
                   (b) every direct requirement (a require line without "// indirect") is
                   named by a `require` line of the rules file, so go.mod has zero
                   requirements or exactly the allowed ones; (c) an "// indirect" requirement
                   is accepted only if at least one direct requirement is present;
                   (d) no replace, exclude, tool, ignore, godebug or retract directive;
                   (e) no go.work and no vendor/ at the root.
                   LIMIT: importlint does not run the go tool, so it cannot tell whether an
                   indirect line is one `go mod tidy` would write. The gate's G0 check
                   `go mod tidy -diff` decides that; the two checks together are the rule
                   "indirect lines only if go mod tidy produces them".
   [nested-module] a go.mod below the root outside the skipped directories (its tree is
                   then not read).
   [layout]        a directory named agent_work, tools or vendor below the top level.
   [parse]         a file whose package clause or import block does not parse (imports that
                   did parse are still checked).
   Example: $BIN/importlint -root . -rules tools/lint-rules/imports.txt

7. boundlint [-allow FILE] -root DIR PKGDIR...
   The bounded-channel lint for the goroutine shell (internal/shell/...). PKGDIR as for
   detlint ("dir/..." recurses; missing directories are skipped silently). Non-test files
   only: a test may use a rendezvous channel. Each package is type-checked with go/types
   (this module from source, the standard library through the source importer).
   Summary on stderr: "boundlint: checked N files in M packages".
   Rules:
   [unbuffered]  make(chan T) with no capacity, or with a capacity that is a constant equal
                 to zero (a literal 0 or a named constant whose value is 0). Allowlistable.
   [chan-cap]    make(chan T, N) where N is not, after removing parentheses, either
                 (a) a named constant: an identifier or pkg.Name that denotes a declared
                     const; or
                 (b) a config field: a field selection x.F where the type of x (through one
                     pointer) is a named struct type whose name contains "config" in any
                     letter case, or one declared in a package named config.
                 Not allowlistable: name the number instead.
                 LIMIT: arithmetic on a constant (2*cap), a conversion (int(cfg.N)), a local
                 variable copied from a config field and a function result are all findings.
   [unbounded-growth]  inside the body of a `for` statement with no condition (`for { }`,
                 `for ;; post { }`) or of a `for ... range` over a channel, at any depth
                 (function literals included): an assignment `x.f = append(...)` (any
                 assignment whose left side is a struct field and whose matching right side
                 is a call of the builtin append), or an assignment or ++/-- to `x.f[k]`
                 where x.f is a struct field of map type -- unless the enclosing top-level
                 function contains a comparison (< <= > >= == !=) one side of which is
                 len(x.f), with x.f spelled the same way. Allowlistable.
                 LIMIT: the comparison only has to exist in the function; the lint does not
                 prove that it guards the insertion. A length check in a helper
                 (`if b.full()`) is not seen: allowlist the function and say where the bound
                 is. Growth through a local alias (q := &b.queue), through a method call
                 (b.queue.Push), in a loop with a condition, or in a function the loop calls
                 is not seen.
   [buildtag]    a non-test .go file the default build leaves out (its channels cannot be
                 checked).   [typecheck] [parse]: as for detlint.
   -allow lines: relative/file.go<TAB>Func<TAB>rule-id<TAB>justification, as for detlint,
     rule-id unbuffered or unbounded-growth (any other rule-id -> [allow-format]). One line
     covers every finding of that rule in that function. Unused line -> [stale-allow].
   Example: $BIN/boundlint -root . -allow tools/lint-rules/boundlint-allow.txt internal/shell/...

Tests (on the host): cd tools/detlint && gofmt -l . ; go vet ./... && go test ./...
