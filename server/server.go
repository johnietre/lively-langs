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
	if err := MakeCmd().Execute(); err != nil {
		log.Fatal(err)
	}
}

func MakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "server",
		Run:                   run,
		DisableFlagsInUseLine: true,
	}

	flags := cmd.Flags()
	flags.IP("ip", net.IPv4(127, 0, 0, 1), "IP address to use")
	flags.Uint16("port", 8000, "Port to use")
	flags.String("db", "lively-langs.db", "Path to database")
	flags.String("templates", "./templates", "Path to templates dir")
	flags.String("static", "./static", "Path to static dir")
	flags.String("log", "", "Log file (empty = stderr)")

	return cmd
}

func run(cmd *cobra.Command, _ []string) {
	log.SetFlags(0)

	flags := cmd.Flags()

	logFile, _ := flags.GetString("log")
	if logFile != "" {
		f, err := jtutils.OpenAppend(logFile)
		if err != nil {
			log.Fatal("error opening log file: ", err)
		}
		log.SetOutput(f)
	}

	ip, _ := flags.GetIP("ip")
	port, _ := flags.GetUint16("port")

	srvr := &Server{
		DbPath:     jtutils.First(flags.GetString("db")),
		TmplsPath:  jtutils.First(flags.GetString("templates")),
		StaticPath: jtutils.First(flags.GetString("static")),
	}
	if err := srvr.Init(); err != nil {
		log.Fatal("error initializing server: ", err)
	}
	addr := &net.TCPAddr{
		IP:   ip,
		Port: int(port),
	}
	if err := srvr.RunTCP(addr); err != nil {
		log.Fatal("error running server: ", err)
	}
}

type Server struct {
	DbPath     string
	TmplsPath  string
	StaticPath string

	db    *DB
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
	lang, err := Lang{}, error(nil)
	if id, e := strconv.ParseInt(name, 10, 64); e == nil {
		lang, err = s.db.getLangById(id)
	} else {
		lang, err = s.db.getLang(name, aliases...)
	}
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
			c.BadRequest(errRespJson("invalid JSON"))
		} else {
			log.Print("error reading json: ", err)
			c.InternalServerError(errRespJson("internal server error"))
		}
		return
	}
	code, resp := http.StatusOK, Response[Lang]{}
	if err := s.db.newLang(&lang); err != nil {
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

func (s *Server) editLangHandler(c *jmux.Context) {
	ld := LangDiff{}
	if err := c.ReadBodyJSON(&ld); err != nil {
		if jtutils.IsUnmarshalError(err) {
			c.BadRequest(errRespJson("invalid JSON"))
		} else {
			log.Print("error reading json: ", err)
			c.InternalServerError(errRespJson("internal server error"))
		}
	}
	code, resp := http.StatusOK, Response[LangDiff]{}
	if err := s.db.editLang(&ld); err != nil {
		if isUserError(err) {
			code, resp.Error = http.StatusBadRequest, err.Error()
		} else {
			log.Printf("error editing lang %d: %v", ld.Id, err)
			code, resp.Error = http.StatusInternalServerError, "internal server error"
		}
		return
	} else {
		resp.Content = ld
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
	aliases := c.Query()["alias"]
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
		word, err = s.db.getWord(lang, wordStr, like, aliases...)
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
	aliases := c.Query()["alias"]
	if word != "" || id != "" || len(aliases) != 0 {
		if id != "" {
			word = id
		}
		if word == "" {
			word = aliases[0]
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
			c.BadRequest(errRespJson("invalid JSON"))
		} else {
			log.Print("error reading json: ", err)
			c.InternalServerError(errRespJson("internal server error"))
		}
		return
	}

	code, resp := http.StatusOK, Response[Word]{}
	if err := s.db.addWord(lang, &word); err != nil {
		if isUserError(err) {
			code, resp.Error = http.StatusBadRequest, err.Error()
		} else {
			log.Printf("error adding to lang %s: %v", word.Word, err)
			code, resp.Error = http.StatusInternalServerError, "internal server error"
		}
		return
	} else {
		resp.Content = word
	}
	c.WriteHeader(code)
	c.WriteJSON(resp)
}

func (s *Server) editWordHandler(c *jmux.Context) {
	lang := c.Params["lang"]
	wd := WordDiff{}
	if err := c.ReadBodyJSON(&wd); err != nil {
		if jtutils.IsUnmarshalError(err) {
			c.BadRequest(errRespJson("invalid JSON"))
		} else {
			log.Print("error reading json: ", err)
			c.InternalServerError(errRespJson("internal server error"))
		}
	}
	code, resp := http.StatusOK, Response[WordDiff]{}
	if err := s.db.editWord(lang, &wd); err != nil {
		if isUserError(err) {
			code, resp.Error = http.StatusBadRequest, err.Error()
		} else {
			log.Printf("error editing word %d in lang %s: %v", wd.Id, lang, err)
			code, resp.Error = http.StatusInternalServerError, "internal server error"
		}
		return
	} else {
		resp.Content = wd
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
		stmt, args := `SELECT * FROM languages WHERE name=?`, []any{what}
		if alias {
			stmt = `SELECT * FROM languages WHERE ? aliases LIKE %|` + what + `|%`
			args = []any{}
			/*
			   stmt = `SELECT * FROM languages WHERE ? aliases LIKE ?`
			   what = "%|"+what+"|%"
			*/
		}
		row := db.QueryRow(stmt, args...)
		lang, err := scanLang(row)
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNoLangFound
		}
		return lang, err
	}

	lang, err := tryGet(name, false)
	for i := 0; i < len(aliases) && err == ErrNoLangFound; i++ {
		lang, err = tryGet(aliases[i], true)
	}
	return lang, err
}

func (db *DB) getLangById(id int64) (Lang, error) {
	row := db.QueryRow(`SELECT * FROM languages WHERE id=?`, id)
	lang, err := scanLang(row)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoLangFound
	}
	return lang, err
}

// Gets the name of the language's database table
func (db *DB) getLangName(name string, aliases ...string) (string, error) {
	if id, err := strconv.ParseInt(name, 10, 64); err == nil {
		row := db.QueryRow(`SELECT id FROM languages WHERE id=?`, id)
		err := row.Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNoLangFound
		}
		return strconv.FormatInt(id, 10), err
	}
	tryGet := func(what string, alias bool) (string, error) {
		stmt, args := `SELECT id FROM languages WHERE name=?`, []any{what}
		if alias {
			stmt = `SELECT id FROM languages WHERE ? aliases LIKE %|` + what + `|%`
			args = []any{}
		}
		row, id := db.QueryRow(stmt, args...), int64(0)
		err := row.Scan(&id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = ErrNoLangFound
			}
		}
		return strconv.FormatInt(id, 10), err
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
		lang, e := scanLang(rows)
		if e != nil {
			if err == nil {
				err = e
			}
		} else {
			langs = append(langs, lang)
		}
	}
	return langs, err
}

func (db *DB) newLang(lang *Lang) error {
	newLang := Lang{
		Name: strings.ToLower(strings.TrimSpace(lang.Name)),
		Aliases: jtutils.FilterMapSlice(
			lang.Aliases,
			func(s string) (string, bool) {
				s = strings.TrimSpace(s)
				return s, len(s) != 0
			},
		),
		Notes: strings.TrimSpace(lang.Notes),
		Words: lang.Words,
	}
	if newLang.Name == "" {
		return ErrInvalidLang
	}

	insStmt, args := lang.toInsertParts()
	res, err := db.Exec(insStmt, args...)
	if err != nil {
		if isUniqueError(err) {
			err = ErrLangExists
		}
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	newLang.Id = id

	tblStmt := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS [%s] (
  id INTEGER PRIMARY KEY,
  word TEXT NOT NULL,
  definition TEXT NOT NULL,
  notes TEXT
);
  `, lang.Name)
	*lang = newLang
	return jtutils.Second(db.Exec(tblStmt))
}

func (db *DB) editLang(ld *LangDiff) error {
	*ld.Name = normalizeWord(*ld.Name)
	if wordIsValid(*ld.Name) {
		return ErrInvalidLang
	}

	stmt, args := ld.toUpdateParts()
	if stmt == "" {
		return nil
	}
	err := jtutils.Second(db.Exec(stmt, args...))
	if isUniqueError(err) {
		err = ErrLangExists
	}
	return err
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

func (db *DB) getWord(
	lang, wordStr string,
	like bool,
	aliases ...string,
) (Word, error) {
	lang, err := db.getLangName(lang)
	if err != nil {
		return Word{}, err
	}

	getWord := func(lang, wordStr string, like, alias bool) (Word, error) {
		stmt, args := "", []any{}
		if !alias {
			if !like {
				stmt = fmt.Sprintf(`SELECT * FROM [%s] WHERE word=?`, lang)
				args = []any{wordStr}
			} else {
				stmt = fmt.Sprintf(
					`SELECT * FROM [%s] WHERE word LIKE %%%s%%`,
					lang, wordStr,
				)
			}
		} else {
			if !like {
				stmt = fmt.Sprintf(
					`SELECT * FROM [%s] WHERE aliases LIKE %%|%s|%%`,
					lang, wordStr,
				)
			} else {
				stmt = fmt.Sprintf(
					`SELECT * FROM [%s] WHERE aliases LIKE %%%s%%`,
					lang, wordStr,
				)
			}
		}
		row := db.QueryRow(stmt, args...)
		word, err := scanWord(row)
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNoWordFound
		}
		return word, err
	}

	word, err := getWord(lang, wordStr, like, false)
	for i := 0; i < len(aliases) && errors.Is(err, ErrNoWordFound); i++ {
		word, err = getWord(lang, aliases[i], like, true)
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
	word, err := scanWord(row)
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
		word, e := scanWord(rows)
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
		Word:       strings.TrimSpace(word.Word),
		Definition: strings.TrimSpace(word.Definition),
		Notes:      strings.TrimSpace(word.Notes),
	}
	if !newWord.wordIsValid() {
		return ErrInvalidWord
	}

	lang, err := db.getLangName(lang)
	if err != nil {
		return err
	}

	stmt, args := newWord.toInsertParts(lang)
	res, err := db.Exec(stmt, args...)
	if err != nil {
		return err
	}
	newWord.Id, err = res.LastInsertId()
	if err == nil {
		*word = newWord
	}
	return err
}

func (db *DB) editWord(lang string, wd *WordDiff) error {
	*wd.Word = normalizeWord(*wd.Word)
	if wordIsValid(*wd.Word) {
		return ErrInvalidWord
	}

	lang, err := db.getLangName(lang)
	if err != nil {
		return err
	}

	stmt, args := wd.toUpdateParts(lang)
	if stmt == "" {
		return nil
	}
	return jtutils.Second(db.Exec(stmt, args...))
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
	ErrLangExists  = fmt.Errorf("language already exists")
	ErrNoLangFound = fmt.Errorf("no language found")
	ErrInvalidLang = fmt.Errorf("invalid language")
	ErrNoWordFound = fmt.Errorf("no word found")
	ErrInvalidWord = fmt.Errorf("invalid word")
)

type Lang struct {
	Id      int64
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Notes   string   `json:"notes,omitempty"`
	Words   []string `json:"words,omitempty"`
}

func scanLang(dbs DBScanner) (lang Lang, err error) {
	aliasesStr := ""
	err = dbs.Scan(&lang.Id, &lang.Name, &aliasesStr, &lang.Notes)
	lang.Aliases = aliasesFromStr(aliasesStr)
	return
}

func (l Lang) toInsertParts() (string, []any) {
	stmt := `INSERT INTO languages VALUES (?,?,?)`
	return stmt, []any{l.Name, aliasesToStr(l.Aliases), l.Notes}
}

func (l Lang) tableName() string {
	//return l.Name
	return strconv.FormatInt(l.Id, 10)
}

type LangDiff struct {
	Id      int64     `json:"id,omitempty"`
	Name    *string   `json:"name,omitempty"`
	Aliases *[]string `json:"aliases,omitempty"`
	Notes   *string   `json:"notes,omitempty"`
}

func (ld LangDiff) toUpdateParts() (string, []any) {
	args, setStmt := []any{}, ""
	if ld.Name != nil {
		args = append(args, *ld.Name)
		setStmt += ", name=?"
	}
	if ld.Aliases != nil {
		args = append(args, aliasesToStr(*ld.Aliases))
		setStmt += ", aliases=?"
	}
	if ld.Notes != nil {
		args = append(args, *ld.Notes)
		setStmt += ", notes=?"
	}
	if len(setStmt) == 0 {
		return "", nil
	} else {
		setStmt = setStmt[1:]
	}
	args = append(args, ld.Id)
	stmt := fmt.Sprintf(`UPDATE languages SET %s WHERE id=?`, setStmt)
	return stmt, args
}

type Word struct {
	Id         int64    `json:"id,omitempty"`
	Word       string   `json:"word"`
	Definition string   `json:"definition"`
	Aliases    []string `json:"aliases,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

func scanWord(dbs DBScanner) (word Word, err error) {
	aliasesStr := ""
	err = dbs.Scan(&word.Id, &word.Word, &word.Definition, &aliasesStr, &word.Notes)
	word.Aliases = aliasesFromStr(aliasesStr)
	return
}

func (w Word) toInsertParts(lang string) (string, []any) {
	stmt := fmt.Sprintf(
		`INSERT INTO [%s](word,description,aliases,notes) VALUES (?,?,?,?)`,
		lang,
	)
	return stmt, []any{w.Word, w.Definition, aliasesToStr(w.Aliases), w.Notes}
}

// Expects word to be trimmed.
func (w Word) wordIsValid() bool {
	return wordIsValid(w.Word)
}

type WordDiff struct {
	Id         int64     `json:"id,omitempty"`
	Word       *string   `json:"word,omitempty"`
	Definition *string   `json:"definition,omitempty"`
	Aliases    *[]string `json:"aliases,omitempty"`
	Notes      *string   `json:"notes,omitempty"`
}

func (wd WordDiff) toUpdateParts(lang string) (string, []any) {
	args, setStmt := []any{}, ""
	if wd.Word != nil {
		args = append(args, *wd.Word)
		setStmt += ", word=?"
	}
	if wd.Definition != nil {
		args = append(args, *wd.Definition)
		setStmt += ", definition=?"
	}
	if wd.Aliases != nil {
		args = append(args, aliasesToStr(*wd.Aliases))
		setStmt += ", aliases=?"
	}
	if wd.Notes != nil {
		args = append(args, *wd.Notes)
		setStmt += ", notes=?"
	}
	if len(setStmt) == 0 {
		return "", nil
	} else {
		setStmt = setStmt[1:]
	}
	args = append(args, wd.Id)
	stmt := fmt.Sprintf(`UPDATE [%s] SET %s WHERE id=?`, setStmt, lang)
	return stmt, args
}

func normalizeWord(word string) string {
	return strings.ToLower(strings.TrimSpace(word))
}

func wordIsValid(word string) bool {
	if len(word) == 0 {
		return false
	}
	isValid := false
	for i, r := range word {
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

// type TemplateMap map[string]*template.Template
type TemplateMap struct {
	index *template.Template
}

func (tm TemplateMap) Index() *template.Template {
	//return tm["index.html"]
	return tm.index
}

type Response[T any] struct {
	Content T      `json:"content"`
	Error   string `json:"error,omitempty"`
}

func errRespJson(errMsg string) string {
	return fmt.Sprintf(`{"error": %q`, errMsg)
}

func aliasesFromStr(s string) []string {
	return jtutils.FilterSliceInPlace(
		strings.Split(s, "|"),
		func(s string) bool { return len(s) != 0 },
	)
}

func aliasesToStr(aliases []string) string {
	aliasesStr := strings.Join(
		jtutils.FilterMapSlice(
			aliases,
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
	return aliasesStr
}

type DBScanner interface {
	Scan(...any) error
}

func errAs[T error](err error) (T, bool) {
	if err == nil {
		var t T
		return t, false
	}
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

func isUniqueError(err error) bool {
	if se, ok := errAs[*sqlite3.Error](err); ok {
		if se.ExtendedCode == sqlite3.ErrConstraintUnique {
			return true
		}
	}
	return false
}
