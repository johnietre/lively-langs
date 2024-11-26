// TOOD: add word alias support
package server

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	jmux "github.com/johnietre/go-jmux"
	jtutils "github.com/johnietre/utils/go"
	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
)

func Run() {
}

func MakeCmd() *cobra.Command {
  cmd := &cobra.Command{
    Use: "server",
    Run: run,
    DisableFlagsInUseLine: true,
  }

  flags := cmd.Flags()
  flags.IP("ip", net.IPv4(127, 0, 0, 1), "IP address to use")
  flags.Uint16("port", 8000, "Port to use")
  flags.String("db", "lively-langs.db", "Path to database")
  flags.String("templates", "./templates", "Path to templates dir")
  flags.String("static", "./static", "Path to static dir")

  return cmd
}

func run(cmd *cobra.Command, _ []string) {
  flags := cmd.Flags()

  ip, _ := flags.GetIP("ip")
  port, _ := flags.GetUint16("port")

  srvr := &Server{
    DbPath: jtutils.First(flags.GetString("db")),
    TmplsPath: jtutils.First(flags.GetString("templates")),
    StaticPath: jtutils.First(flags.GetString("static")),
  }
  if err := srvr.Init(); err != nil {
    log.Fatal("error initializing server: ", err)
  }
  addr := &net.TCPAddr{
    IP: ip,
    Port: int(port),
  }
  if err := srvr.RunTCP(addr); err != nil {
    log.Fatal("error running server: ", err)
  }
}

type Server struct {
  DbPath string
  TmplsPath string
  StaticPath string

  db *DB
  tmpls *jtutils.AValue[TemplateMap]

  srvr *http.Server
}

func (s *Server) Init() error {
  shouldRun := jtutils.NewT(true)
  deferrer := jtutils.NewDeferredFunc(shouldRun)
  defer deferrer.Run()

  if _, err := os.Stat(s.StaticPath); err != nil {
    return fmt.Errorf("error checking static path: %v", err)
  }

  if _, err := os.Stat(s.TmplsPath); err != nil {
    return fmt.Errorf("error checking templates path: %v", err)
  }
  tm, err := loadTmpls(s.TmplsPath)
  if err != nil {
    return err
  }
  s.tmpls = jtutils.NewAValue(tm)

  db, err := openDb(s.DbPath)
  if err != nil {
    return err
  }
  deferrer.Add(func() { db.Close() })

  s.srvr = &http.Server{
    Handler: s.createHandler(),
  }

  *shouldRun = false
  return nil
}

func (s *Server) createHandler() http.Handler {
  r := jmux.NewRouter()

  r.GetFunc("/", s.homeHandler)

  r.GetFunc("/langs", s.getLangsHandler)
  r.GetFunc("/langs/{lang}", s.getLangHandler)
  r.PostFunc("/langs", s.newLangHandler)
  r.DeleteFunc("/langs/{lang}", s.delLangHandler)

  r.GetFunc("/langs/{lang}/words", s.getWordsHandler)
  r.GetFunc("/langs/{lang}/words/{word}", s.getWordHandler)
  r.PostFunc("/langs/{lang}/words", s.addWordHandler)
  r.DeleteFunc("/langs/{lang}/words/{id}", s.delWordHandler)

  r.Get(
    "/static/",
    jmux.WrapH(http.StripPrefix(
      "/static", http.FileServer(http.Dir(s.StaticPath)),
    )),
  ).MatchAny(jmux.MethodsGet())

  return r
}

func (s *Server) RunTCP(addr *net.TCPAddr) error {
  if s.srvr == nil {
    return fmt.Errorf("server must be initialized first")
  }
  ln, err := net.ListenTCP("tcp", addr)
  if err != nil {
    return err
  }
  log.Printf("running server on %s", addr)
  return s.srvr.Serve(ln)
}

func (s *Server) homeHandler(c *jmux.Context) {
  tmpl, ok := s.tmpls.LoadSafe()
  if !ok {
    log.Print("no index template stored")
    c.InternalServerError("internal server error")
    return
  }
  if err := tmpl.Index().Execute(c.Writer, nil); err != nil {
    log.Print("error executing template: ", err)
  }
}

func (s *Server) getLangHandler(c *jmux.Context) {
  name := c.Params["lang"]
  aliases := c.Query()["alias"]
  lang, err := s.db.getLang(name, aliases...)
  code, resp := http.StatusOK, Response[Lang]{}
  if err != nil {
    if isUserError(err) {
      code, resp.Error = http.StatusBadRequest, err.Error()
    } else {
      log.Printf("error getting lang %s: %v", name, err)
      code, resp.Error = http.StatusInternalServerError, "internal server error"
    }
  } else {
    resp.Content = lang
  }
  c.WriteHeader(code)
  c.WriteJSON(resp)
}

func (s *Server) getLangsHandler(c *jmux.Context) {
  name := c.Query().Get("name")
  if name != "" || len(c.Query()["alias"]) != 0 {
    if name == "" {
      name = c.Query().Get("alias")
    }
    // FIXME: use Context clone method when added to jmux.
    newC := jtutils.NewT(*c)
    newC.Params = jtutils.CloneMap(c.Params)
    newC.Params["lang"] = name
    s.getLangHandler(newC)
    return
  }

  langs, err := s.db.getLangs()
  code, resp := http.StatusOK, Response[[]Lang]{}
  if err != nil {
    log.Printf("error gettings langs: %v", err)
    if langs == nil {
      code = http.StatusInternalServerError
      resp.Error = "internal server error"
    } else {
      resp.Error = "partial internal server error"
    }
  }
  resp.Content = langs
  c.WriteHeader(code)
  c.WriteJSON(resp)
}

func (s *Server) newLangHandler(c *jmux.Context) {
  lang := Lang{}
  if err := c.ReadBodyJSON(&lang); err != nil {
    if jtutils.IsUnmarshalError(err) {
      c.BadRequest("invalid JSON")
    } else {
      log.Print("error reading json: ", err)
      c.InternalServerError("internal server error")
    }
    return
  }
  code, resp := http.StatusOK, Response[Lang]{}
  if err := s.db.newLang(lang); err != nil {
    if isUserError(err) {
      code, resp.Error = http.StatusBadRequest, err.Error()
    } else {
      log.Printf("error adding lang %s: %v", lang.Name, err)
      code, resp.Error = http.StatusInternalServerError, "internal server error"
    }
    return
  } else {
    resp.Content = lang
  }
  c.WriteHeader(code)
  c.WriteJSON(resp)
}

func (s *Server) delLangHandler(c *jmux.Context) {
  name := c.Params["lang"]
  lang, err := s.db.delLang(name)
  code, resp := http.StatusOK, Response[Lang]{}
  if err != nil {
    if isUserError(err) {
      code, resp.Error = http.StatusBadRequest, err.Error()
    } else {
      log.Printf("error deleting lang %s: %v", name, err)
      code, resp.Error = http.StatusInternalServerError, "internal server error"
    }
  } else {
    resp.Content = lang
  }
  c.WriteHeader(code)
  c.WriteJSON(resp)
}

func (s *Server) getWordHandler(c *jmux.Context) {
  lang, wordStr := c.Params["lang"], c.Params["word"]
  likeStr, like := c.Query().Get("like"), false
  if b, err := strconv.ParseBool(likeStr); err != nil {
    c.WriteError(http.StatusBadRequest, "invalid value for 'like'")
    return
  } else {
    like = b
  }
  word, err := Word{}, error(nil)
  if id, e := strconv.ParseInt(wordStr, 10, 64); e == nil {
    word, err = s.db.getWordById(lang, id)
  } else {
    word, err = s.db.getWord(lang, wordStr, like)
  }
  code, resp := http.StatusOK, Response[Word]{}
  if err != nil {
    if isUserError(err) {
      code, resp.Error = http.StatusBadRequest, err.Error()
    } else {
      log.Printf("error getting word %s from lang %s: %v", lang, wordStr, err)
      code, resp.Error = http.StatusInternalServerError, "internal server error"
    }
  } else {
    resp.Content = word
  }
  c.WriteHeader(code)
  c.WriteJSON(resp)
}

func (s *Server) getWordsHandler(c *jmux.Context) {
  lang := c.Params["lang"]
  word, id := c.Query().Get("word"), c.Query().Get("id")
  if word != "" || id != "" {
    if id != "" {
      word = id
    }
    newC := jtutils.NewT(*c)
    newC.Params = jtutils.CloneMap(c.Params)
    newC.Params["word"] = word
    s.getWordHandler(newC)
    return
  }

  words, err := s.db.getAllWords(lang)
  code, resp := http.StatusOK, Response[[]Word]{}
  if err != nil {
    if isUserError(err) {
      code, resp.Error = http.StatusBadRequest, err.Error()
    } else if words == nil {
      log.Printf("error getting words for lang %s: %v", lang, err)
      code, resp.Error = http.StatusInternalServerError, "internal server error"
    } else {
      resp.Error = "partial internal server error"
    }
  }
  resp.Content = words
  c.WriteHeader(code)
  c.WriteJSON(resp)
}

func (s *Server) addWordHandler(c *jmux.Context) {
  lang := c.Params["lang"]
  word := Word{}
  if err := c.ReadBodyJSON(&word); err != nil {
    if jtutils.IsUnmarshalError(err) {
      c.BadRequest("invalid JSON")
    } else {
      log.Print("error reading json: ", err)
      c.InternalServerError("internal server error")
    }
    return
  }

  code, resp := http.StatusOK, Response[Word]{}
  if err := s.db.addWord(lang, &word); err != nil {
    if isUserError(err) {
      code, resp.Error = http.StatusBadRequest, err.Error()
    } else {
      log.Printf("error adding lang %s: %v", word.Word, err)
      code, resp.Error = http.StatusInternalServerError, "internal server error"
    }
    return
  } else {
    resp.Content = word
  }
  c.WriteHeader(code)
  c.WriteJSON(resp)
}

func (s *Server) delWordHandler(c *jmux.Context) {
  lang, idStr := c.Params["lang"], c.Params["id"]
  id, err := strconv.ParseInt(idStr, 10, 64)
  if err != nil {
    return
  }
  word, err := s.db.delWordById(lang, id)
  code, resp := http.StatusOK, Response[Word]{}
  if err != nil {
    if isUserError(err) {
      code, resp.Error = http.StatusBadRequest, err.Error()
    } else {
      log.Printf("error deleting word with ID %d: %v", id, err)
      code, resp.Error = http.StatusInternalServerError, "internal server error"
    }
  } else {
    resp.Content = word
  }
  c.WriteHeader(code)
  c.WriteJSON(resp)
}

func loadTmpls(path string) (TemplateMap, error) {
  indexPath := filepath.Join(path, "index.html")
  tmpl := template.New("").Delims("{|", "|}")
  tmpl, err := tmpl.ParseFiles(indexPath)
  if err != nil {
    return TemplateMap{}, err
  }
  tm := TemplateMap{
    index: tmpl,
  }
  return tm, nil
}

func openDb(path string) (*DB, error) {
  sqlDb, err := sql.Open("go-sqlite3", path)
  if err != nil {
    return nil, err
  }
  db := &DB{sqlDb}
  if err := db.Init(); err != nil {
    db.Close()
    return nil, err
  }
  return db, nil
}

type DB struct {
  *sql.DB
}

func (db *DB) Init() error {
  const initStmt = `
CREATE TABLE IF NOT EXISTS languages (
  language TEXT NOT NULL UNIQUE,
  aliases TEXT
);
  `
  return jtutils.Second(db.Exec(initStmt))
}

func (db *DB) getLang(name string, aliases ...string) (Lang, error) {
  tryGet := func(what string, alias bool) (Lang, error) {
    stmt := `SELECT * FROM languages WHERE name=?`
    if alias {
      stmt = `SELECT * FROM languages WHERE ? aliases LIKE ?`
      what = "%|"+what+"|%"
    }
    row := db.QueryRow(stmt, what)
    lang, aliasesStr := Lang{}, ""
    err := row.Scan(&lang.Name, &aliasesStr, &lang.Notes)
    if err != nil {
      if errors.Is(err, sql.ErrNoRows) {
        err = ErrNoLangFound
      }
    } else {
      lang.Aliases = jtutils.FilterSliceInPlace(
        strings.Split(aliasesStr, "|"),
        func(s string) bool { return len(s) != 0 },
      )
    }
    return lang, err
  }

  lang, err := tryGet(name, false)
  for i := 0; i < len(aliases) && err == ErrNoLangFound; i++ {
    lang, err = tryGet(aliases[i], true)
  }
  return lang, err
}

func (db *DB) getLangName(name string, aliases ...string) (string, error) {
  tryGet := func(what string, alias bool) (string, error) {
    stmt := `SELECT name FROM languages WHERE name=?`
    if alias {
      stmt = `SELECT * FROM languages WHERE ? aliases LIKE ?`
      what = "%|"+what+"|%"
    }
    row, name := db.QueryRow(stmt, what), ""
    err := row.Scan(&name)
    if err != nil {
      if errors.Is(err, sql.ErrNoRows) {
        err = ErrNoLangFound
      }
    }
    return name, err
  }

  langName, err := tryGet(name, false)
  for i := 0; i < len(aliases) && err == ErrNoLangFound; i++ {
    langName, err = tryGet(aliases[i], true)
  }
  return langName, err
}

func (db *DB) getLangs() ([]Lang, error) {
  stmt := fmt.Sprint(`SELECT * FROM languages`)
  rows, err := db.Query(stmt)
  if err != nil {
    return nil, err
  }
  defer rows.Close()

  langs := []Lang{}
  for rows.Next() {
    lang, aliasesStr := Lang{}, ""
    e := rows.Scan(&lang.Name, &aliasesStr, &lang.Notes)
    if e != nil {
      if err == nil {
        err = e
      }
    } else {
      lang.Aliases = aliasesFromStr(aliasesStr)
      langs = append(langs, lang)
    }
  }
  return langs, err
}

func (db *DB) newLang(lang Lang) error {
  name := strings.ToLower(strings.TrimSpace(lang.Name))
  if name == "" {
    return ErrInvalidLang
  }
  aliasesStr := strings.Join(
    jtutils.FilterMapSlice(
      lang.Aliases,
      func(s string) (string, bool) {
        s = strings.TrimSpace(s)
        return s, len(s) != 0
      },
    ),
    "|",
  )
  if aliasesStr != "" {
    aliasesStr = "|" + aliasesStr + "|"
  }
  notes := strings.TrimSpace(lang.Notes)

  insStmt := `INSERT INTO languages VALUES (?,?,?)`
  if _, err := db.Exec(insStmt, name, aliasesStr, notes); err != nil {
    if se, ok := errAs[*sqlite3.Error](err); ok {
      if se.ExtendedCode == sqlite3.ErrConstraintUnique {
        err = ErrLangExists
      }
    }
    return err
  }

  tblStmt := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS [%s] (
  id INTEGER PRIMARY KEY,
  word TEXT NOT NULL,
  definition TEXT NOT NULL,
  notes TEXT
);
  `, lang)
  return jtutils.Second(db.Exec(tblStmt))
}

func (db *DB) delLang(name string) (Lang, error) {
  lang, err := db.getLang(name)
  if err != nil {
    return lang, err
  }
  stmt := `DELETE FROM languages WHERE name=?`
  res, err := db.Exec(stmt, name)
  if err != nil {
    return lang, err
  }
  if n, err := res.RowsAffected(); err != nil {
    return lang, err
  } else if n == 0 {
    return lang, nil
  }
  dropStmt := fmt.Sprintf(`DROP TABLE [%s]`, name)
  return lang, jtutils.Second(db.Exec(dropStmt))
}

func (db *DB) getWord(lang string, wordStr string, like bool) (Word, error) {
  lang, err := db.getLangName(lang)
  if err != nil {
    return Word{}, err
  }

  stmt, args := "", []any{}
  if !like {
    stmt = fmt.Sprintf(`SELECT * FROM [%s] WHERE word=?`, lang)
    args = []any{wordStr}
  } else {
    stmt = fmt.Sprintf(
      `SELECT * FROM [%s] WHERE word LIKE %%%s%%`,
      lang, wordStr,
    )
  }
  row := db.QueryRow(stmt, args...)
  word := Word{}
  err = row.Scan(&word.Id, &word.Word, &word.Definition, &word.Notes)
  if err != nil {
    if errors.Is(err, sql.ErrNoRows) {
      err = ErrNoWordFound
    }
  }
  return word, nil
}

func (db *DB) getWordById(lang string, id int64) (Word, error) {
  lang, err := db.getLangName(lang)
  if err != nil {
    return Word{}, err
  }

  stmt := fmt.Sprintf(`SELECT * FROM [%s] WHERE id=?`, lang)
  row := db.QueryRow(stmt, id)
  word := Word{}
  err = row.Scan(&word.Id, &word.Word, &word.Definition, &word.Notes)
  if err != nil {
    if errors.Is(err, sql.ErrNoRows) {
      err = ErrNoWordFound
    }
  }
  return word, nil
}

func (db *DB) getAllWords(lang string) ([]Word, error) {
  lang, err := db.getLangName(lang)
  if err != nil {
    return nil, err
  }

  stmt := fmt.Sprintf(`SELECT * FROM [%s]`, lang)
  rows, err := db.Query(stmt)
  if err != nil {
    return nil, err
  }
  defer rows.Close()

  words := []Word{}
  for rows.Next() {
    word := Word{}
    e := rows.Scan(&word.Id, &word.Word, &word.Definition, &word.Notes)
    if e != nil {
      if err == nil {
        err = e
      }
    } else {
      words = append(words, word)
    }
  }
  return words, err
}

func (db *DB) addWord(lang string, word *Word) error {
  newWord := Word{
    Word: strings.TrimSpace(word.Word),
    Definition: strings.TrimSpace(word.Definition),
    Notes: strings.TrimSpace(word.Notes),
  }
  if !newWord.wordIsValid() {
    return ErrInvalidWord
  }

  lang, err := db.getLangName(lang)
  if err != nil {
    return err
  }

  stmt := fmt.Sprintf(
    `INSERT INTO [%s](word,description,notes) VALUES (?,?,?)`,
    lang,
  )
  res, err := db.Exec(stmt, word.Word, word.Definition, word.Notes)
  if err != nil {
    return err
  }
  newWord.Id, err = res.LastInsertId()
  if err == nil {
    *word = newWord
  }
  return err
}

func (db *DB) delWordById(lang string, id int64) (Word, error) {
  word, err := db.getWordById(lang, id)
  if err != nil {
    return word, err
  }
  stmt := fmt.Sprintf(`DELETE FROM [%s] WHERE id=?`, lang)
  _, err = db.Exec(stmt, id)
  return word, err
}

var (
  ErrLangExists = fmt.Errorf("language already exists")
  ErrNoLangFound = fmt.Errorf("no language found")
  ErrInvalidLang = fmt.Errorf("invalid language")
  ErrNoWordFound = fmt.Errorf("no word found")
  ErrInvalidWord = fmt.Errorf("invalid word")
)

type Lang struct {
  Name string `json:"name"`
  Aliases []string `json:"aliases,omitempty"`
  Notes string `json:"notes,omitempty"`
  Words []string `json:"words,omitempty"`
}

type Word struct {
  Id int64 `json:"id,omitempty"`
  Word string `json:"word"`
  Definition string `json:"definition"`
  Aliases []string `json:"aliases"`
  Notes string `json:"notes,omitempty"`
}

// Expects word to be trimmed.
func (w Word) wordIsValid() bool {
  if len(w.Word) == 0 {
    return false
  }
  isValid := false
  for i, r := range w.Word {
    if r >= '0' && r <= '9' {
      if i == 0 {
        return false
      }
    } else if !unicode.IsSpace(r) {
      isValid = true
    }
  }
  return isValid
}

//type TemplateMap map[string]*template.Template
type TemplateMap struct {
  index *template.Template
}

func (tm TemplateMap) Index() *template.Template {
  //return tm["index.html"]
  return tm.index
}

type Response[T any] struct {
  Content T `json:"content"`
  Error string `json:"error,omitempty"`
}

func aliasesFromStr(s string) []string {
  return jtutils.FilterSliceInPlace(
    strings.Split(s, "|"),
    func(s string) bool { return len(s) != 0 },
  )
}

func errAs[T error](err error) (T, bool) {
  e, ok := err.(T)
  return e, ok
}

func isUserError(err error) bool {
  return errors.Is(err, ErrLangExists) ||
    errors.Is(err, ErrNoLangFound) ||
    errors.Is(err, ErrInvalidLang) ||
    errors.Is(err, ErrNoWordFound) ||
    errors.Is(err, ErrInvalidWord)
}
