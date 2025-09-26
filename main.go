package main

import (
    "bufio"
    "errors"
    "flag"
    "fmt"
    "io/ioutil"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "sort"
    "strings"

    "gopkg.in/yaml.v3"
)

type Config struct {
    Cwd        string
    InputDir   string
    OutputDir  string
    RootFile   string
    RootPath   string
    PathsDir   string
    SchemasDir string
    ParamsDir  string

    OutputTS string
    OutputGo string
    Redocly  string // html output path (docs)

    // Redocly bundle
    BundleOut     string
    RedoclyConfig string

    // Optional: generator overrides
    TSGenerator string // e.g. typescript-fetch
    GoGenerator string // e.g. go

    // Behavior
    Join bool // if true, write joined/inlined root; default false = reference-style

    // Validation
    ValidatePreset   string // validation preset to use
    SkipValidation   bool   // skip validation entirely
    ValidateStopOnError bool // stop on first validation error
}

func envOrDefault(key, def string) string {
    v := strings.TrimSpace(os.Getenv(key))
    if v != "" {
        return v
    }
    return def
}

func buildConfig() (*Config, error) {
    var (
        inputDirFlag  = flag.String("input", "", "[required] Source OpenAPI fragments directory")
        inputDirFlagS = flag.String("i", "", "Shorthand for --input")
        outputDirFlag = flag.String("output", "", "[required] Destination directory for the generated root file")
        outputDirFlagS= flag.String("o", "", "Shorthand for --output")
        rootFileFlag  = flag.String("root", "", "Name of the aggregated root file (default: root.yaml)")
        rootFileFlagS = flag.String("r", "", "Shorthand for --root")

        outputTS      = flag.String("output-ts", "", "If set, generate TypeScript output using an installed OpenAPI tool to this path")
        outputGo      = flag.String("output-go", "", "If set, generate Go output using an installed OpenAPI tool to this path")
        redoclyOut    = flag.String("redocly", "", "If set, generate HTML docs using installed 'redocly' CLI to this file")
        bundleOut     = flag.String("bundle", "", "If set, bundle the spec using Redocly CLI to this YAML file")
        redoclyCfg    = flag.String("redocly-config", "", "Optional Redocly configuration file path (default: ./redocly.yaml if present)")

        tsGen         = flag.String("ts-generator", envOrDefault("TS_GENERATOR", "typescript-fetch"), "Generator name for OpenAPI generator when producing TS (default: typescript-fetch)")
        goGen         = flag.String("go-generator", envOrDefault("GO_GENERATOR", "go"), "Generator name for OpenAPI generator when producing Go (default: go)")

        joinOutput    = flag.Bool("join", false, "Write joined/inlined root instead of reference-style")
        allDo         = flag.Bool("all", false, "Bundle to dist/openapi.yaml and build HTML to dist/index.html (uses --redocly-config if present)")

        // Validation flags
        validatePreset = flag.String("validate", "", "Run validation with specified preset (google, restful)")
        skipValidation = flag.Bool("skip-validation", false, "Skip validation entirely")
        validateStopOnError = flag.Bool("validate-stop-on-error", false, "Stop on first validation error")
        listPresets    = flag.Bool("list-presets", false, "List available validation presets")
    )

    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "sync-openapi\n\n")
        fmt.Fprintf(os.Stderr, "Usage:\n  sync-openapi --input <dir> [--output <dir>] [--root <file>] [--bundle <yaml>] [--redocly <html>] [--all] [--validate <preset>]\n\n")
        fmt.Fprintf(os.Stderr, "Options:\n")
        fmt.Fprintf(os.Stderr, "  -i, --input <dir>      [required] Source OpenAPI fragments directory\n")
        fmt.Fprintf(os.Stderr, "  -o, --output <dir>     Destination dir for root file (default: same as --input)\n")
        fmt.Fprintf(os.Stderr, "  -r, --root <file>      Name of the aggregated root file (default: root.yaml)\n")
        fmt.Fprintf(os.Stderr, "      --output-ts <p>    Generate TypeScript output using installed OpenAPI tool to the given path\n")
        fmt.Fprintf(os.Stderr, "      --output-go <p>    Generate Go output using installed OpenAPI tool to the given path\n")
        fmt.Fprintf(os.Stderr, "      --redocly <html>   Generate HTML docs using installed Redocly CLI to this file\n")
        fmt.Fprintf(os.Stderr, "      --ts-generator <g> Generator for TypeScript when using openapi-generator (default: typescript-fetch)\n")
        fmt.Fprintf(os.Stderr, "      --go-generator <g> Generator for Go when using openapi-generator (default: go)\n")
        fmt.Fprintf(os.Stderr, "      --join            Write joined/inlined root instead of reference-style\n")
        fmt.Fprintf(os.Stderr, "      --bundle <yaml>   Bundle the spec using Redocly CLI to the given YAML path\n")
        fmt.Fprintf(os.Stderr, "      --redocly-config <file> Optional Redocly config (default: ./redocly.yaml if present)\n")
        fmt.Fprintf(os.Stderr, "      --all             Do both: bundle -> dist/openapi.yaml and HTML -> dist/index.html\n")
        fmt.Fprintf(os.Stderr, "\n")
        fmt.Fprintf(os.Stderr, "Validation Options:\n")
        fmt.Fprintf(os.Stderr, "      --validate <preset>         Run validation with specified preset (google, restful)\n")
        fmt.Fprintf(os.Stderr, "      --skip-validation          Skip validation entirely\n")
        fmt.Fprintf(os.Stderr, "      --validate-stop-on-error   Stop on first validation error\n")
        fmt.Fprintf(os.Stderr, "      --list-presets            List available validation presets\n")
    }

    flag.Parse()

    // Handle list presets request
    if *listPresets {
        listAvailablePresets()
        return nil, nil // Signal to exit without error
    }

    // Determine input/output/root from flags or env
    inputDir := firstNonEmpty(*inputDirFlag, *inputDirFlagS)
    outputDir := firstNonEmpty(*outputDirFlag, *outputDirFlagS)
    if strings.TrimSpace(inputDir) == "" {
        flag.Usage()
        return nil, errors.New("missing required flag: --input is required")
    }
    // If output dir not provided, default to input dir so root.yaml lives alongside fragments.
    if strings.TrimSpace(outputDir) == "" { outputDir = inputDir }
    rootFile := firstNonEmpty(*rootFileFlag, *rootFileFlagS)
    if strings.TrimSpace(rootFile) == "" {
        rootFile = "root.yaml"
    }

    cwd, _ := os.Getwd()
    inputDir = absJoin(cwd, inputDir)
    outputDir = absJoin(cwd, outputDir)
    rootPath := absJoin(outputDir, rootFile)

    // Determine default Redocly config if not provided
    redoclyConfig := strings.TrimSpace(*redoclyCfg)
    if redoclyConfig == "" {
        def := filepath.Join(cwd, "redocly.yaml")
        if st, err := os.Stat(def); err == nil && !st.IsDir() {
            redoclyConfig = def
        }
    }

    cfg := &Config{
        Cwd:        cwd,
        InputDir:   inputDir,
        OutputDir:  outputDir,
        RootFile:   rootFile,
        RootPath:   rootPath,
        PathsDir:   filepath.Join(inputDir, "paths"),
        SchemasDir: filepath.Join(inputDir, "components", "schemas"),
        ParamsDir:  filepath.Join(inputDir, "components", "parameters"),
        OutputTS:   strings.TrimSpace(*outputTS),
        OutputGo:   strings.TrimSpace(*outputGo),
        Redocly:    strings.TrimSpace(*redoclyOut),
        BundleOut:  strings.TrimSpace(*bundleOut),
        RedoclyConfig: redoclyConfig,
        TSGenerator: strings.TrimSpace(*tsGen),
        GoGenerator: strings.TrimSpace(*goGen),
        Join:       *joinOutput,
        ValidatePreset: strings.TrimSpace(*validatePreset),
        SkipValidation: *skipValidation,
        ValidateStopOnError: *validateStopOnError,
    }

    if *allDo {
        if cfg.BundleOut == "" { cfg.BundleOut = absJoin(cwd, filepath.Join("dist", "openapi.yaml")) }
        if cfg.Redocly == "" { cfg.Redocly = absJoin(cwd, filepath.Join("dist", "index.html")) }
    }

    return cfg, nil
}

func firstNonEmpty(vals ...string) string {
    for _, v := range vals {
        if strings.TrimSpace(v) != "" {
            return v
        }
    }
    return ""
}

func absJoin(base, p string) string {
    if filepath.IsAbs(p) {
        return filepath.Clean(p)
    }
    return filepath.Clean(filepath.Join(base, p))
}

func ensureDir(dir string) error {
    if dir == "" { return errors.New("empty dir path") }
    return os.MkdirAll(dir, 0o755)
}

func listYAMLFiles(root string) ([]string, error) {
    var files []string
    if st, err := os.Stat(root); err != nil || !st.IsDir() {
        return files, nil
    }
    err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
        if err != nil { return err }
        name := d.Name()
        if strings.HasPrefix(name, ".") { // skip dot files/dirs
            if d.IsDir() && path != root {
                return filepath.SkipDir
            }
        }
        if d.Type().IsRegular() && strings.HasSuffix(strings.ToLower(name), ".yaml") {
            files = append(files, path)
        }
        return nil
    })
    return files, err
}

func relFrom(baseDir, target string) string {
    rel, err := filepath.Rel(baseDir, target)
    if err != nil {
        return target
    }
    rel = filepath.ToSlash(rel)
    if rel == "" || rel == "." {
        return "./"
    }
    if strings.HasPrefix(rel, ".") || strings.HasPrefix(rel, "/") {
        return rel
    }
    return "./" + rel
}

// String helpers similar to the JS version
func kebabToCamel(s string) string {
    // convert kebab-case to camelCase
    out := ""
    up := false
    for i := 0; i < len(s); i++ {
        c := s[i]
        if c == '-' || c == '_' || c == ' ' {
            up = true
            continue
        }
        if up {
            out += strings.ToUpper(string(c))
            up = false
        } else {
            out += string(c)
        }
    }
    return out
}

func pascalCase(s string) string {
    camel := kebabToCamel(s)
    if camel == "" { return camel }
    return strings.ToUpper(camel[:1]) + camel[1:]
}

// Build the aggregated root YAML (with $ref entries) without third-party YAML libs
func writeRootYAML(cfg *Config) error {
    // Reference-style root (legacy)
    if err := ensureDir(filepath.Dir(cfg.RootPath)); err != nil { return err }

    paths, err := listYAMLFiles(cfg.PathsDir)
    if err != nil { return err }
    schemas, err := listYAMLFiles(cfg.SchemasDir)
    if err != nil { return err }
    params, err := listYAMLFiles(cfg.ParamsDir)
    if err != nil { return err }

    // Stable ordering
    sort.Strings(paths)
    sort.Strings(schemas)
    sort.Strings(params)

    f, err := os.Create(cfg.RootPath)
    if err != nil { return err }
    defer f.Close()
    w := bufio.NewWriter(f)

    // Minimal header; users can edit final file later if needed
    // Note: keeping it simple to avoid external YAML lib
    fmt.Fprintln(w, "openapi: \"3.0.0\"")
    fmt.Fprintln(w, "info:")
    fmt.Fprintln(w, "  title: API")
    fmt.Fprintln(w, "  version: \"1.0.0\"")

    // paths
    fmt.Fprintln(w, "paths:")
    for _, p := range paths {
        key := buildPathKey(cfg.PathsDir, p)
        if key == "" { continue }
        ref := relFrom(filepath.Dir(cfg.RootPath), p)
        fmt.Fprintf(w, "  %s:\n", key)
        fmt.Fprintf(w, "    $ref: %s\n", ref)
    }

    // components
    fmt.Fprintln(w, "components:")
    // schemas
    fmt.Fprintln(w, "  schemas:")
    for _, s := range schemas {
        base := strings.TrimSuffix(filepath.Base(s), ".yaml")
        name := pascalCase(base)
        ref := relFrom(filepath.Dir(cfg.RootPath), s)
        fmt.Fprintf(w, "    %s:\n", name)
        fmt.Fprintf(w, "      $ref: %s\n", ref)
    }
    // parameters
    fmt.Fprintln(w, "  parameters:")
    for _, p := range params {
        base := strings.TrimSuffix(filepath.Base(p), ".yaml")
        name := pascalCase(base)
        ref := relFrom(filepath.Dir(cfg.RootPath), p)
        fmt.Fprintf(w, "    %s:\n", name)
        fmt.Fprintf(w, "      $ref: %s\n", ref)
    }

    if err := w.Flush(); err != nil { return err }
    return nil
}

// Helper: read file as string
func readText(path string) (string, error) {
    b, err := ioutil.ReadFile(path)
    if err != nil { return "", err }
    return string(b), nil
}

func indentText(s string, spaces int) string {
    pad := strings.Repeat(" ", spaces)
    lines := strings.Split(s, "\n")
    for i, ln := range lines {
        if ln == "" {
            // keep empty lines empty for cleaner YAML
            continue
        }
        lines[i] = pad + ln
    }
    // Trim a single trailing newline for consistency
    out := strings.Join(lines, "\n")
    out = strings.TrimRight(out, "\n")
    return out + "\n"
}

// Build a map for schema and parameter file basenames to canonical names
func buildNameMap(dir string) map[string]string {
    m := map[string]string{}
    files, _ := listYAMLFiles(dir)
    for _, f := range files {
        base := strings.TrimSuffix(filepath.Base(f), ".yaml")
        name := pascalCase(base)
        m[strings.ToLower(base)] = name
        m[strings.ToLower(filepath.Base(f))] = name
    }
    return m
}

var (
    reSchemaPath   = regexp.MustCompile(`(?i)(?:^|.*/)(?:components/)?schemas/([^/#\s]+)\.ya?ml$`)
    reParamPath    = regexp.MustCompile(`(?i)(?:^|.*/)(?:components/)?parameters/([^/#\s]+)\.ya?ml$`)
)

func stripQuotes(s string) string {
    s = strings.TrimSpace(s)
    if len(s) >= 2 {
        if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
            return s[1:len(s)-1]
        }
    }
    return s
}

func rewriteRefs(raw string, schemaMap, paramMap map[string]string) string {
    lines := strings.Split(raw, "\n")
    for i, ln := range lines {
        idx := strings.Index(ln, "$ref:")
        if idx < 0 { continue }
        // split into indent+key and value
        left := ln[:idx]
        rest := strings.TrimSpace(ln[idx+len("$ref:"):])
        if rest == "" { continue }
        val := stripQuotes(rest)
        // Already internal
        if strings.HasPrefix(val, "#/components/") {
            continue
        }
        // pseudo forms
        low := strings.ToLower(val)
        if strings.HasPrefix(low, "schema:") {
            base := strings.TrimSpace(val[len("schema:"):])
            name := pascalCase(base)
            lines[i] = left + "$ref: #/components/schemas/" + name
            continue
        }
        if strings.HasPrefix(low, "param:") {
            base := strings.TrimSpace(val[len("param:"):])
            name := pascalCase(base)
            lines[i] = left + "$ref: #/components/parameters/" + name
            continue
        }
        // file path style
        if m := reSchemaPath.FindStringSubmatch(val); len(m) == 2 {
            base := strings.ToLower(m[1])
            name := schemaMap[base]
            if name == "" { name = pascalCase(m[1]) }
            lines[i] = left + "$ref: #/components/schemas/" + name
            continue
        }
        if m := reParamPath.FindStringSubmatch(val); len(m) == 2 {
            base := strings.ToLower(m[1])
            name := paramMap[base]
            if name == "" { name = pascalCase(m[1]) }
            lines[i] = left + "$ref: #/components/parameters/" + name
            continue
        }
        // else leave as-is
    }
    return strings.Join(lines, "\n")
}

// Joined/inlined root
func writeRootJoinedYAML(cfg *Config) error {
    if err := ensureDir(filepath.Dir(cfg.RootPath)); err != nil { return err }

    paths, err := listYAMLFiles(cfg.PathsDir)
    if err != nil { return err }
    schemas, err := listYAMLFiles(cfg.SchemasDir)
    if err != nil { return err }
    params, err := listYAMLFiles(cfg.ParamsDir)
    if err != nil { return err }

    sort.Strings(paths)
    sort.Strings(schemas)
    sort.Strings(params)

    schemaMap := buildNameMap(cfg.SchemasDir)
    paramMap := buildNameMap(cfg.ParamsDir)

    f, err := os.Create(cfg.RootPath)
    if err != nil { return err }
    defer f.Close()
    w := bufio.NewWriter(f)

    // Header
    fmt.Fprintln(w, "openapi: \"3.0.0\"")
    fmt.Fprintln(w, "info:")
    fmt.Fprintln(w, "  title: API")
    fmt.Fprintln(w, "  version: \"1.0.0\"")

    // paths
    fmt.Fprintln(w, "paths:")
    for _, p := range paths {
        key := buildPathKey(cfg.PathsDir, p)
        if key == "" { continue }
        fmt.Fprintf(w, "  %s:\n", key)
        content, err := readText(p)
        if err != nil { return err }
        content = rewriteRefs(content, schemaMap, paramMap)
        fmt.Fprint(w, indentText(content, 4))
    }

    // components
    fmt.Fprintln(w, "components:")
    // schemas
    fmt.Fprintln(w, "  schemas:")
    for _, s := range schemas {
        base := strings.TrimSuffix(filepath.Base(s), ".yaml")
        name := pascalCase(base)
        fmt.Fprintf(w, "    %s:\n", name)
        content, err := readText(s)
        if err != nil { return err }
        content = rewriteRefs(content, schemaMap, paramMap)
        fmt.Fprint(w, indentText(content, 6))
    }
    // parameters
    fmt.Fprintln(w, "  parameters:")
    for _, p := range params {
        base := strings.TrimSuffix(filepath.Base(p), ".yaml")
        name := pascalCase(base)
        fmt.Fprintf(w, "    %s:\n", name)
        content, err := readText(p)
        if err != nil { return err }
        content = rewriteRefs(content, schemaMap, paramMap)
        fmt.Fprint(w, indentText(content, 6))
    }

    if err := w.Flush(); err != nil { return err }
    return nil
}

func buildPathKey(pathsDir, fullPath string) string {
    rel, err := filepath.Rel(pathsDir, fullPath)
    if err != nil { return "" }
    rel = filepath.ToSlash(rel)
    segs := strings.Split(rel, "/")
    if len(segs) == 0 { return "" }
    file := segs[len(segs)-1]
    segs = segs[:len(segs)-1]
    nameNoExt := strings.TrimSuffix(file, ".yaml")
    tail := kebabToCamel(nameNoExt)
    // Expect first segment to be version (e.g., v1)
    if len(segs) == 0 {
        // No version folder, just use tail at root
        return "/" + tail
    }
    version := segs[0]
    remainder := ""
    if len(segs) > 1 {
        remainder = strings.Join(segs[1:], "/")
    }
    if remainder != "" {
        return "/" + version + "/" + remainder + "/" + tail
    }
    return "/" + version + "/" + tail
}

func runCmd(name string, args ...string) error {
    cmd := exec.Command(name, args...)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    cmd.Stdin = os.Stdin
    return cmd.Run()
}

func which(bin string) string {
    p, err := exec.LookPath(bin)
    if err != nil { return "" }
    return p
}

func findRedocly(cwd string) string {
    if p := which("redocly"); p != "" { return p }
    local := filepath.Join(cwd, "node_modules", ".bin", "redocly")
    if st, err := os.Stat(local); err == nil && !st.IsDir() {
        return local
    }
    return ""
}

func generateTypeScript(cfg *Config) error {
    if cfg.OutputTS == "" { return nil }
    // Prefer openapi-generator if available
    if p := which("openapi"); p != "" {
        // Assume syntax: openapi generate -g typescript -i spec -o out
        out := cfg.OutputTS
        // If output is a .ts file and openapi-typescript exists, prefer that
        if strings.HasSuffix(strings.ToLower(out), ".ts") {
            if which("openapi-typescript") != "" {
                return runCmd("openapi-typescript", cfg.RootPath, "-o", out)
            }
            // fallback: inform better path
            fmt.Fprintln(os.Stderr, "Tip: install openapi-typescript for single-file TS types: npm i -g openapi-typescript")
            // Fallback to using openapi as a dir generator by using parent dir
            out = filepath.Dir(out)
        }
        return runCmd("openapi", "generate", "-g", "typescript", "-i", cfg.RootPath, "-o", out)
    }
    if p := which("openapi-generator"); p != "" {
        out := cfg.OutputTS
        if strings.HasSuffix(strings.ToLower(out), ".ts") {
            if which("openapi-typescript") != "" {
                return runCmd("openapi-typescript", cfg.RootPath, "-o", out)
            }
            fmt.Fprintln(os.Stderr, "Tip: install openapi-typescript for single-file TS types: npm i -g openapi-typescript")
            out = filepath.Dir(out)
        }
        gen := cfg.TSGenerator
        if gen == "" { gen = "typescript-fetch" }
        return runCmd("openapi-generator", "generate", "-g", gen, "-i", cfg.RootPath, "-o", out)
    }
    // Not found: provide guidance
    return fmt.Errorf("no OpenAPI generator found. Install one of:\n - brew install openapi-generator\n - npm i -g @openapitools/openapi-generator-cli\n - npm i -g openapi-typescript (for single-file types)")
}

func generateGo(cfg *Config) error {
    if cfg.OutputGo == "" { return nil }
    // Prefer openapi (if present), then openapi-generator, else oapi-codegen for single file
    if which("openapi") != "" {
        out := cfg.OutputGo
        // If .go requested, suggest using oapi-codegen if installed
        if strings.HasSuffix(strings.ToLower(out), ".go") {
            if which("oapi-codegen") != "" {
                pkg := guessPackage(filepath.Dir(out))
                return runCmd("oapi-codegen", "-generate", "types,client,server", "-o", out, "-package", pkg, cfg.RootPath)
            }
            fmt.Fprintln(os.Stderr, "Tip: install oapi-codegen for single-file Go: go install github.com/deepmap/oapi-codegen/v2/cmd/oapi-codegen@latest")
            out = filepath.Dir(out)
        }
        gen := cfg.GoGenerator
        if gen == "" { gen = "go" }
        return runCmd("openapi", "generate", "-g", gen, "-i", cfg.RootPath, "-o", out)
    }
    if which("openapi-generator") != "" {
        out := cfg.OutputGo
        if strings.HasSuffix(strings.ToLower(out), ".go") {
            if which("oapi-codegen") != "" {
                pkg := guessPackage(filepath.Dir(out))
                return runCmd("oapi-codegen", "-generate", "types,client,server", "-o", out, "-package", pkg, cfg.RootPath)
            }
            fmt.Fprintln(os.Stderr, "Tip: install oapi-codegen for single-file Go: go install github.com/deepmap/oapi-codegen/v2/cmd/oapi-codegen@latest")
            out = filepath.Dir(out)
        }
        gen := cfg.GoGenerator
        if gen == "" { gen = "go" }
        return runCmd("openapi-generator", "generate", "-g", gen, "-i", cfg.RootPath, "-o", out)
    }
    // As a last resort, single-file generation with oapi-codegen, if available
    if which("oapi-codegen") != "" {
        out := cfg.OutputGo
        if !strings.HasSuffix(strings.ToLower(out), ".go") {
            // If directory provided, choose default file name
            if err := ensureDir(out); err == nil {
                out = filepath.Join(out, "api.gen.go")
            }
        } else {
            if err := ensureDir(filepath.Dir(out)); err != nil { return err }
        }
        pkg := guessPackage(filepath.Dir(out))
        return runCmd("oapi-codegen", "-generate", "types,client,server", "-o", out, "-package", pkg, cfg.RootPath)
    }
    return fmt.Errorf("no OpenAPI generator found. Install one of:\n - brew install openapi-generator\n - npm i -g @openapitools/openapi-generator-cli\n - go install github.com/deepmap/oapi-codegen/v2/cmd/oapi-codegen@latest")
}

func guessPackage(dir string) string {
    base := filepath.Base(dir)
    base = strings.ReplaceAll(base, "-", "_")
    base = strings.ReplaceAll(base, ".", "_")
    if base == "" || base == "/" || base == "." {
        return "api"
    }
    // must start with letter for package names; fallback if not
    if (base[0] < 'A' || base[0] > 'z') || (base[0] > 'Z' && base[0] < 'a') {
        return "api"
    }
    return base
}

func buildDocsHTML(cfg *Config) error {
    if cfg.Redocly == "" { return nil }
    // Prefer bundled spec for docs if available; fallback to root
    input := cfg.BundleOut
    if strings.TrimSpace(input) == "" { input = cfg.RootPath }

    if exe := findRedocly(cfg.Cwd); exe != "" {
        if err := ensureDir(filepath.Dir(cfg.Redocly)); err != nil { return err }
        args := []string{"build-docs", input, "--output", cfg.Redocly}
        if cfg.RedoclyConfig != "" { args = append(args, "--config", cfg.RedoclyConfig) }
        return runCmd(exe, args...)
    }
    // Try redoc-cli as alternative
    if which("redoc-cli") != "" {
        if err := ensureDir(filepath.Dir(cfg.Redocly)); err != nil { return err }
        return runCmd("redoc-cli", "build", input, "-o", cfg.Redocly)
    }
    return fmt.Errorf("redocly CLI not found. Install with:\n - npm i -g @redocly/cli\nAlternatively, install redoc-cli: npm i -g redoc-cli")
}

func bundleWithRedocly(cfg *Config) error {
    if cfg.BundleOut == "" { return nil }
    exe := findRedocly(cfg.Cwd)
    if exe == "" {
        return fmt.Errorf("redocly CLI not found. Install with one of:\n - npm i -g @redocly/cli\n - npm i -D @redocly/cli (then ensure node_modules/.bin is present)")
    }
    if err := ensureDir(filepath.Dir(cfg.BundleOut)); err != nil { return err }
    args := []string{"bundle", cfg.RootPath, "-o", cfg.BundleOut}
    if cfg.RedoclyConfig != "" {
        args = append(args, "--config", cfg.RedoclyConfig)
    }
    return runCmd(exe, args...)
}

// Validation types and functions

// ValidationRule represents a single validation rule
type ValidationRule struct {
	Name        string
	Description string
	Validate    func(path string, method string, operation map[string]interface{}) error
}

// ValidationPreset represents a collection of validation rules
type ValidationPreset struct {
	Name        string
	Description string
	Rules       []ValidationRule
}

// ValidationResult holds the validation results
type ValidationResult struct {
	Path     string
	Method   string
	Rule     string
	Message  string
	Severity string // "error" or "warning"
}

// ValidationConfig holds validation settings
type ValidationConfig struct {
	Preset      string
	StopOnError bool
	Results     []ValidationResult
}

// Predefined validation presets
var ValidationPresets = map[string]ValidationPreset{
	"google": {
		Name:        "Google API Design Guide",
		Description: "Validation rules based on Google's API Design Guide best practices",
		Rules: []ValidationRule{
			{
				Name:        "http-methods-rest",
				Description: "Use standard HTTP methods (GET, POST, PUT, PATCH, DELETE)",
				Validate:    validateHTTPMethods,
			},
			{
				Name:        "collection-names-plural",
				Description: "Collection names should be plural nouns",
				Validate:    validatePluralCollections,
			},
			{
				Name:        "path-case-kebab",
				Description: "Path segments should use kebab-case (lowercase with dashes)",
				Validate:    validateKebabCase,
			},
			{
				Name:        "no-trailing-slash",
				Description: "Paths should not have trailing slashes",
				Validate:    validateNoTrailingSlash,
			},
			{
				Name:        "operation-id-present",
				Description: "All operations should have operationId",
				Validate:    validateOperationId,
			},
			{
				Name:        "operation-summary-present",
				Description: "All operations should have summary",
				Validate:    validateOperationSummary,
			},
			{
				Name:        "response-200-present",
				Description: "GET operations should have 200 response",
				Validate:    validateGetResponse200,
			},
			{
				Name:        "resource-id-param",
				Description: "Resource paths should use {id} parameter naming",
				Validate:    validateResourceIdParam,
			},
		},
	},
	"restful": {
		Name:        "RESTful API Standards",
		Description: "Common RESTful API design standards",
		Rules: []ValidationRule{
			{
				Name:        "http-methods-rest",
				Description: "Use standard HTTP methods (GET, POST, PUT, PATCH, DELETE)",
				Validate:    validateHTTPMethods,
			},
			{
				Name:        "operation-id-present",
				Description: "All operations should have operationId",
				Validate:    validateOperationId,
			},
			{
				Name:        "collection-names-plural",
				Description: "Collection names should be plural nouns",
				Validate:    validatePluralCollections,
			},
			{
				Name:        "no-trailing-slash",
				Description: "Paths should not have trailing slashes",
				Validate:    validateNoTrailingSlash,
			},
		},
	},
}

// Individual validation functions

func validateHTTPMethods(path string, method string, operation map[string]interface{}) error {
	validMethods := map[string]bool{
		"get":     true,
		"post":    true,
		"put":     true,
		"patch":   true,
		"delete":  true,
		"head":    true,
		"options": true,
	}
	
	if !validMethods[strings.ToLower(method)] {
		return fmt.Errorf("invalid HTTP method '%s', should be one of: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS", method)
	}
	return nil
}

func validatePluralCollections(path string, method string, operation map[string]interface{}) error {
	// Extract path segments, ignoring parameters
	segments := strings.Split(strings.Trim(path, "/"), "/")
	
	// Common singular words that should be plural in API paths
	singularWords := []string{
		"account", "user", "mission", "reward", "partner", "activity", "car", "member",
		"order", "product", "service", "item", "category", "group", "role", "permission",
		"resource", "entity", "record", "document", "file", "image", "video", "comment",
	}
	
	for _, segment := range segments {
		// Skip parameters (enclosed in braces)
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			continue
		}
		
		// Skip version segments
		if matched, _ := regexp.MatchString(`^v\d+$`, segment); matched {
			continue
		}
		
		// Check if segment is a known singular word
		for _, singular := range singularWords {
			if strings.ToLower(segment) == singular {
				plural := makePlural(singular)
				return fmt.Errorf("collection name '%s' should be plural: '%s'", segment, plural)
			}
		}
	}
	return nil
}

func validateKebabCase(path string, method string, operation map[string]interface{}) error {
	// Extract path segments, ignoring parameters
	segments := strings.Split(strings.Trim(path, "/"), "/")
	kebabRegex := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$|^v\d+$|^\{[^}]+\}$`)
	
	for _, segment := range segments {
		if !kebabRegex.MatchString(segment) {
			return fmt.Errorf("path segment '%s' should use kebab-case (lowercase with dashes)", segment)
		}
	}
	return nil
}

func validateNoTrailingSlash(path string, method string, operation map[string]interface{}) error {
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		return fmt.Errorf("path should not have trailing slash")
	}
	return nil
}

func validateOperationId(path string, method string, operation map[string]interface{}) error {
	if _, exists := operation["operationId"]; !exists {
		return fmt.Errorf("operation should have operationId")
	}
	return nil
}

func validateOperationSummary(path string, method string, operation map[string]interface{}) error {
	if _, exists := operation["summary"]; !exists {
		return fmt.Errorf("operation should have summary")
	}
	return nil
}

func validateGetResponse200(path string, method string, operation map[string]interface{}) error {
	if strings.ToLower(method) != "get" {
		return nil // Skip non-GET methods
	}
	
	responses, ok := operation["responses"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("GET operation should have responses defined")
	}
	
	if _, exists := responses["200"]; !exists {
		return fmt.Errorf("GET operation should have 200 response")
	}
	return nil
}

func validateResourceIdParam(path string, method string, operation map[string]interface{}) error {
	// Check for parameter patterns that don't follow {id} convention
	paramRegex := regexp.MustCompile(`\{([^}]+)\}`)
	matches := paramRegex.FindAllStringSubmatch(path, -1)
	
	for _, match := range matches {
		paramName := match[1]
		// Allow common patterns but suggest {id} for simple resource identifiers
		if !isValidParamName(paramName) {
			return fmt.Errorf("parameter '{%s}' should follow naming convention (consider using {id} for resource identifiers)", paramName)
		}
	}
	return nil
}

// Helper functions

func makePlural(word string) string {
	// Simple pluralization rules
	switch {
	case strings.HasSuffix(word, "y"):
		return strings.TrimSuffix(word, "y") + "ies"
	case strings.HasSuffix(word, "s"), strings.HasSuffix(word, "ch"), strings.HasSuffix(word, "sh"):
		return word + "es"
	default:
		return word + "s"
	}
}

func isValidParamName(name string) bool {
	// Allow common parameter naming patterns
	validPatterns := []string{
		"id", "userId", "accountId", "missionId", "partnerId", 
		"carId", "activityId", "rewardId", "memberId",
	}
	
	for _, pattern := range validPatterns {
		if name == pattern {
			return true
		}
	}
	
	// Allow patterns like "xxxId"
	if strings.HasSuffix(name, "Id") && len(name) > 2 {
		return true
	}
	
	return false
}

// Main validation functions

func validatePaths(cfg *Config, validationCfg *ValidationConfig) error {
	preset, exists := ValidationPresets[validationCfg.Preset]
	if !exists {
		return fmt.Errorf("unknown validation preset: %s", validationCfg.Preset)
	}
	
	fmt.Printf("Running validation with preset: %s\n", preset.Name)
	fmt.Printf("Description: %s\n", preset.Description)
	fmt.Printf("Rules: %d\n\n", len(preset.Rules))
	
	paths, err := listYAMLFiles(cfg.PathsDir)
	if err != nil {
		return err
	}
	
	hasErrors := false
	
	for _, pathFile := range paths {
		apiPath := buildPathKey(cfg.PathsDir, pathFile)
		if apiPath == "" {
			continue
		}
		
		// Parse the YAML file to extract operations
		content, err := ioutil.ReadFile(pathFile)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", pathFile, err)
		}
		
		var pathSpec map[string]interface{}
		if err := yaml.Unmarshal(content, &pathSpec); err != nil {
			return fmt.Errorf("failed to parse %s: %w", pathFile, err)
		}
		
		// Validate each HTTP method in the path
		for method, operationRaw := range pathSpec {
			operation, ok := operationRaw.(map[string]interface{})
			if !ok {
				continue // Skip non-operation fields
			}
			
			// Run all validation rules
			for _, rule := range preset.Rules {
				if err := rule.Validate(apiPath, method, operation); err != nil {
					result := ValidationResult{
						Path:     apiPath,
						Method:   strings.ToUpper(method),
						Rule:     rule.Name,
						Message:  err.Error(),
						Severity: "error",
					}
					validationCfg.Results = append(validationCfg.Results, result)
					
					// Print error immediately
					fmt.Printf("❌ %s %s - %s: %s\n", 
						result.Method, 
						result.Path, 
						result.Rule, 
						result.Message)
					
					hasErrors = true
					
					if validationCfg.StopOnError {
						return fmt.Errorf("validation failed on first error")
					}
				}
			}
		}
	}
	
	// Print summary
	if hasErrors {
		fmt.Printf("\n❌ Validation failed with %d error(s)\n", len(validationCfg.Results))
		return errors.New("validation failed")
	} else {
		fmt.Printf("\n✅ All validations passed!\n")
	}
	
	return nil
}

func listAvailablePresets() {
	fmt.Println("Available validation presets:")
	for key, preset := range ValidationPresets {
		fmt.Printf("  %s: %s\n", key, preset.Description)
		fmt.Printf("    Rules: %d\n", len(preset.Rules))
	}
}

func run(cfg *Config) error {
    if err := ensureDir(cfg.OutputDir); err != nil { return err }

    // Run validation first if configured
    if !cfg.SkipValidation && cfg.ValidatePreset != "" {
        validationCfg := &ValidationConfig{
            Preset:      cfg.ValidatePreset,
            StopOnError: cfg.ValidateStopOnError,
            Results:     []ValidationResult{},
        }
        
        if err := validatePaths(cfg, validationCfg); err != nil {
            return fmt.Errorf("validation failed: %w", err)
        }
        fmt.Println() // Add spacing after validation
    }

    if cfg.Join {
        if err := writeRootJoinedYAML(cfg); err != nil {
            return fmt.Errorf("building joined root YAML: %w", err)
        }
    } else {
        if err := writeRootYAML(cfg); err != nil {
            return fmt.Errorf("building reference-style root YAML: %w", err)
        }
    }
    fmt.Fprintf(os.Stdout, "Wrote root spec: %s\n", cfg.RootPath)

    if err := generateTypeScript(cfg); err != nil {
        return err
    }
    if err := generateGo(cfg); err != nil {
        return err
    }
    if err := bundleWithRedocly(cfg); err != nil { return err }
    if err := buildDocsHTML(cfg); err != nil {
        return err
    }
    return nil
}

func main() {
    cfg, err := buildConfig()
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    
    // Handle special case where we just listed presets
    if cfg == nil {
        return
    }
    
    if err := run(cfg); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

