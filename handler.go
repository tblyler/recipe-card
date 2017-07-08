package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve"
	blevemapping "github.com/blevesearch/bleve/mapping"
	log "github.com/sirupsen/logrus"
	"github.com/tblyler/recipe-card/recipe"
)

const (
	imagePattern     = "/images/"
	stockImagePatten = "/stock-images/"
	recipePattern    = "/recipe/"
	docxPattern      = "/docx/"
)

// Handler contains functions for http handlerfunc
type Handler struct {
	recipePath  string
	recipes     map[string]*recipe.Recipe
	recipeSlice []*recipe.Recipe
	idx         bleve.Index
	templates   *template.Template
	logger      *log.Logger
}

func GetItemIndex(path string) (map[string][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer file.Close()

	reader := bufio.NewReader(file)

	itemIndex := make(map[string][]byte)
	for {
		data, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return itemIndex, nil
			}

			return nil, err
		}

		// remove newline
		key := string(data[:len(data)-1])
		sha256sum := make([]byte, sha256.Size)

		read, err := reader.Read(sha256sum)
		if err != nil && read != sha256.Size {
			return nil, err
		}

		itemIndex[key] = sha256sum
		if err == io.EOF {
			break
		}
	}

	return itemIndex, nil
}

func SaveItemIndex(itemIndex map[string][]byte, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}

	defer file.Close()

	for key, sha256sum := range itemIndex {
		_, err = file.WriteString(key + "\n")
		if err != nil {
			return err
		}

		_, err = file.Write(sha256sum)
		if err != nil {
			return err
		}
	}

	return nil
}

// NewHandler creates a new instance to handle HTTP requests
func NewHandler(recipePath string, indexPath string, logger *log.Logger) (*Handler, error) {
	if logger == nil {
		logger = log.New()
		logger.Out = ioutil.Discard
	}

	logger.WithField("recipePath", recipePath).Debugln("Getting absolute recipe path")

	var err error
	recipePath, err = filepath.Abs(recipePath)
	if err != nil {
		return nil, fmt.Errorf("Failed absolute recipe path: %s", err.Error())
	}

	logger.WithField("recipePath", recipePath).Debugln("Got absolute recipe path")

	if indexPath != "" {
		logger.WithField("indexPath", indexPath).Debugln("Getting absolute index path")
		indexPath, err = filepath.Abs(indexPath)
		if err != nil {
			return nil, fmt.Errorf("Failed absolute index path: %s", err.Error())
		}

		logger.WithField("indexPath", indexPath).Debugln("Got absolute index path")
		os.MkdirAll(indexPath, 0755)
	}

	logger.WithField("recipePath", recipePath).Infoln("Getting recipes from path")
	recipeSlice, err := recipe.RecipesFromPath(recipePath)
	if err != nil {
		return nil, err
	}

	logger.Infof("Found %d recipes", len(recipeSlice))

	handler := new(Handler)
	handler.logger = logger
	handler.recipePath = recipePath
	handler.recipeSlice = recipeSlice
	handler.recipes = make(map[string]*recipe.Recipe)
	bleveIndexPath := ""
	itemIndexPath := ""
	// this improves indexing performance a shit ton
	// I don't think it stores the document data, just analysis data
	// could be wrong, documentation is sparse for it
	// functionality seems the same for here though
	blevemapping.StoreDynamic = false

	if indexPath == "" {
		logger.Info("Creating memory mapped search index")
		handler.idx, err = bleve.NewMemOnly(bleve.NewIndexMapping())
	} else {
		itemIndexPath = filepath.Join(indexPath, "item.idx")
		bleveIndexPath = filepath.Join(indexPath, "bleve")
		logger.WithField("bleveIndexPath", bleveIndexPath).Infoln("Trying to open index path")

		handler.idx, err = bleve.Open(bleveIndexPath)
		if err != nil {
			logger.WithError(err).WithField("bleveIndexPath", bleveIndexPath).Warnln(
				"Failed to open index path, trying to recreate it",
			)

			handler.idx, err = bleve.New(bleveIndexPath, bleve.NewIndexMapping())
		}
	}

	if err != nil {
		return nil, fmt.Errorf("Bleve open: %s", err.Error())
	}

	var itemIndex map[string][]byte

	if itemIndexPath != "" {
		logger.WithField("itemIndexPath", itemIndexPath).Debugln("Trying to open previous item index")
		itemIndex, err = GetItemIndex(itemIndexPath)
		if err != nil {
			logger.WithError(err).WithField("itemIndexPath", itemIndexPath).Warnln("Failed to open previous item index")
		} else {
			logger.WithField("count", len(itemIndex)).Debugln("Got previous item indexes")
		}
	}

	if itemIndex == nil {
		itemIndex = make(map[string][]byte)
	}

	for _, recip := range recipeSlice {
		if recip.Title == "" {
			logger.WithField("docx", recip.DocxPath).Errorln(
				"Missing title",
			)
			continue
		}

		if oldRecip, exists := handler.recipes[recip.Title]; exists {
			logger.WithFields(log.Fields{
				"existingPath": oldRecip.DocxPath,
				"newPath":      recip.DocxPath,
				"title":        recip.Title,
			}).Errorln("Duplicate recipe title")
			continue
		}

		handler.recipes[recip.Title] = recip

		logger.WithField("recipeTitle", recip.Title).Debugln("Hashing data")
		hasher := sha256.New()
		io.WriteString(hasher, recip.Title)
		for _, order := range recipe.ValidCategoriesOrder {
			if info, exists := recip.Info[order]; exists {
				for _, line := range info {
					io.WriteString(hasher, line)
				}
			}
		}

		sha256sum := hasher.Sum(nil)

		logger.WithFields(log.Fields{
			"recipeTitle": recip.Title,
			"sha256":      hex.EncodeToString(sha256sum),
		}).Debugln("Finished hashing data")

		if oldSha, exists := itemIndex[recip.Title]; !exists || !bytes.Equal(sha256sum, oldSha) {
			logger.WithFields(log.Fields{
				"recipeTitle": recip.Title,
				"docx":        recip.DocxPath,
			}).Infoln("Indexing")

			if exists {
				handler.idx.Delete(recip.Title)
			}

			itemIndex[recip.Title] = sha256sum
			err = handler.idx.Index(recip.Title, recip)
			if err != nil {
				return nil, fmt.Errorf("Index fail: %s", err.Error())
			}

			logger.WithField("recipeTitle", recip.Title).Infoln("Indexed")
		}
	}

	for recipeTitle := range itemIndex {
		if _, exists := handler.recipes[recipeTitle]; !exists {
			logger.WithField("recipeTitle", recipeTitle).Infoln("Removing missing recipe")
			handler.idx.Delete(recipeTitle)
			delete(itemIndex, recipeTitle)
		}
	}

	err = SaveItemIndex(itemIndex, itemIndexPath)
	if err != nil {
		logger.WithError(err).WithField("itemIndexPath", itemIndexPath).Warnln(
			"Failed to update index data",
		)
	} else {
		logger.Infoln("Updated index data")
	}

	handler.templates, err = NewTemplate(logger)
	if err != nil {
		return nil, err
	}

	return handler, nil
}

// Close handler and free up memory
func (h *Handler) Close() error {
	h.recipes = nil
	return h.idx.Close()
}

// GetHandlerFuncs in a pattern->func map
func (h *Handler) GetHandlerFuncs() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/":              h.Index,
		"/search/":       h.Search,
		"/recipes/":      h.Recipes,
		recipePattern:    h.Recipe,
		"/css/mini.css":  h.MiniCSS,
		"/css/main.css":  h.MainCSS,
		imagePattern:     h.Images,
		stockImagePatten: h.StockImages,
		docxPattern:      h.Docx,
	}
}

// MainCSS outputs the main css file information
func (h *Handler) MainCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css")

	io.WriteString(w, maincss)
}

// MiniCSS writes the mini css data to writer
func (h *Handler) MiniCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css")

	io.WriteString(w, minicss)
}

// Index handles index request
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	tmplData := &TemplateData{
		PageTitle: "Recipe Card",
	}

	h.templates.ExecuteTemplate(w, "index", tmplData)
}

// Search handles search request
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	search := strings.TrimSpace(r.PostFormValue("search"))
	if search == "" {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	tmplData := &TemplateData{
		PageTitle:   "Recipe Card - Search",
		SearchValue: search,
	}

	searchResults, _ := h.idx.Search(bleve.NewSearchRequest(bleve.NewMatchQuery(
		search,
	)))

	// try a fuzzy search if matchquery fails
	if searchResults.Hits.Len() == 0 {
		searchResults, _ = h.idx.Search(bleve.NewSearchRequest(bleve.NewFuzzyQuery(
			search,
		)))
	}

	for _, hit := range searchResults.Hits {
		recipe := h.recipes[hit.ID]

		tmplData.Recipes = append(
			tmplData.Recipes,
			h.recipeToTemplateRecipe(recipe),
		)
	}

	h.templates.ExecuteTemplate(w, "search", tmplData)
}

// Recipes handles recipes page for all recipes
func (h *Handler) Recipes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	tmplData := &TemplateData{
		PageTitle: "Recipe Card - Recipes",
	}

	for _, recipe := range h.recipeSlice {
		tmplData.Recipes = append(
			tmplData.Recipes,
			h.recipeToTemplateRecipe(recipe),
		)
	}

	h.templates.ExecuteTemplate(w, "recipes", tmplData)
}

// Recipe handles a single recipe page
func (h *Handler) Recipe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	id := strings.TrimPrefix(r.URL.Path, recipePattern)

	if recipe, exists := h.recipes[id]; exists {
		tmplData := &TemplateData{
			PageTitle: "Recipe Card - " + id,
		}

		tmplData.Recipes = append(
			tmplData.Recipes,
			h.recipeToTemplateRecipe(recipe),
		)

		h.templates.ExecuteTemplate(w, "recipe", tmplData)
		return
	}

	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

// StockImages handles all stock image requests
func (h *Handler) StockImages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/jpeg")

	id := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, ".jpg"), stockImagePatten)
	if recipe, exists := h.recipes[id]; exists {
		w.Write(recipe.Image)
		return
	}

	w.WriteHeader(http.StatusNotFound)
	return
}

// Docx handles all docx download requests
func (h *Handler) Docx(w http.ResponseWriter, r *http.Request) {
	lowerPath := strings.ToLower(r.URL.Path)

	if !strings.HasSuffix(lowerPath, "docx") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	file, err := os.Open(h.urlToPath(r.URL.Path, docxPattern))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	defer file.Close()

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	io.Copy(w, file)
}

// Images handles all image requests
func (h *Handler) Images(w http.ResponseWriter, r *http.Request) {
	lowerPath := strings.ToLower(r.URL.Path)

	if !strings.HasSuffix(lowerPath, "jpg") && !strings.HasSuffix(lowerPath, "jpeg") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	file, err := os.Open(h.urlToPath(r.URL.Path, imagePattern))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	defer file.Close()

	w.Header().Set("Content-Type", "image/jpeg")
	io.Copy(w, file)
}

func (h *Handler) pathToURL(filePath, pattern string) (string, error) {
	path, err := filepath.Rel(h.recipePath, filePath)
	if err != nil {
		return "", err
	}

	urlPath := pattern[:len(pattern)-1]
	for _, pathPart := range strings.Split(path, string(filepath.Separator)) {
		if pathPart == "" {
			continue
		}

		urlPath += "/" + url.PathEscape(pathPart)
	}

	return urlPath, nil
}

func (h *Handler) urlToPath(url, pattern string) string {
	path := filepath.Join(
		h.recipePath,
		strings.Replace(strings.TrimPrefix(url, pattern), "/", string(filepath.Separator), -1),
	)

	if !filepath.IsAbs(path) {
		log.WithFields(log.Fields{
			"url":     url,
			"pattern": pattern,
			"path":    path,
		}).Errorln("Must only receive absolute path")
		return ""
	}

	return path
}

// recipeToTemplateRecipe converts a recipe.Recipe to a TemplateRecipe
func (h *Handler) recipeToTemplateRecipe(rec *recipe.Recipe) *TemplateRecipe {
	tmplRecipe := &TemplateRecipe{
		ID:         rec.Title,
		URL:        "/recipe/" + url.PathEscape(rec.Title),
		StockImage: stockImagePatten + url.PathEscape(rec.Title+".jpg"),
	}

	docxURL, err := h.pathToURL(rec.DocxPath, docxPattern)
	if err != nil {
		log.WithError(err).WithField("docxPath", rec.DocxPath).Warnln(
			"Failed to get docx url",
		)
	} else {
		tmplRecipe.DocxURL = docxURL
	}

	for _, imagePath := range rec.ScanPaths {
		urlPath, err := h.pathToURL(imagePath, imagePattern)
		if err != nil {
			continue
		}

		tmplRecipe.Images = append(
			tmplRecipe.Images,
			urlPath,
		)
	}

	for _, category := range recipe.ValidCategoriesOrder {
		if info, exists := rec.Info[category]; exists {
			description := ""
			for _, infoLine := range info {
				description += "<p>" + html.EscapeString(infoLine) + "</p>"
			}

			tmplRecipe.Description += template.HTML(fmt.Sprintf(
				"<h3>%s</h3>%s",
				html.EscapeString(category),
				description,
			))
		}
	}

	return tmplRecipe
}
