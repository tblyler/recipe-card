package recipe

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tblyler/goatomic"
	"github.com/tblyler/recipe-card/doc"
)

// ValidCategoriesOrder defines keys of info in order of importance
var ValidCategoriesOrder = []string{
	"serves",
	"oven temperature",
	"ingredients",
	"preparation",
	"tips",
}

// validCategories defines keys of Info
var validCategories = map[string]bool{
	"oven temperature": true,
	"serves":           true,
	"ingredients":      true,
	"preparation":      true,
	"tips":             true,
}

// Recipe stores information regarding a specific recipe
type Recipe struct {
	Title string              `json:"title"`
	Info  map[string][]string `json:"info"`
	// FIXME support non-docx
	DocxPath  string   `json:"docx_path"`
	ScanPaths []string `json:"scan_paths"`
	Image     []byte
}

// Summary outputs a nice summary of Info
func (r *Recipe) Summary() (output string) {
	for _, category := range ValidCategoriesOrder {
		if info, exists := r.Info[category]; exists {
			if output != "" {
				// add an extra newline between categories
				output += "\n"
			}

			output += category + "\n" + strings.Join(info, "\n")
		}
	}

	return
}

// ParseFiles for the recipe
func (r *Recipe) ParseFiles() error {
	dir := filepath.Dir(r.DocxPath)

	// get a list of recipe scans
	infos, err := ioutil.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, info := range infos {
		if info.IsDir() {
			continue
		}

		name := strings.ToLower(info.Name())
		// FIXME support non-jpeg
		if !strings.HasSuffix(name, ".jpeg") && !strings.HasSuffix(name, ".jpg") {
			continue
		}

		r.ScanPaths = append(r.ScanPaths, filepath.Join(dir, info.Name()))
	}

	sort.Strings(r.ScanPaths)

	file, err := os.Open(r.DocxPath)
	if err != nil {
		return err
	}

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	docx, err := doc.NewDocx(file, stat.Size())
	if err != nil {
		return err
	}

	r.Image = docx.Image

	lines, err := docx.Text()
	if err != nil {
		return err
	}

	r.Info = make(map[string][]string)

	titleIsNext := false
	currentGroup := ""
	for _, line := range lines {
		if r.Title == "" {
			if titleIsNext {
				r.Title = line
				continue
			}

			if strings.Contains(strings.ToLower(line), "recipe") {
				titleIsNext = true
			}

			continue
		}

		lowerLine := strings.ToLower(strings.Replace(line, ":", "", -1))
		if _, exists := validCategories[lowerLine]; exists {
			currentGroup = lowerLine
			continue
		}

		// make sure a current group is set
		if currentGroup == "" {
			continue
		}

		r.Info[currentGroup] = append(r.Info[currentGroup], line)
	}

	return nil
}

// RecipesFromPath generates Recipe instances from a path
func RecipesFromPath(dirPath string) (recipes []*Recipe, err error) {
	// get the absolute path of the directory and clean it
	dirPath, err = filepath.Abs(dirPath)
	if err != nil {
		return
	}

	stat, err := os.Stat(dirPath)
	if err != nil {
		return
	}

	if !stat.IsDir() {
		return nil, fmt.Errorf("Not a directory %s", dirPath)
	}

	err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		// skip directories and non-docx files
		// FIXME support non-docx
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".docx") {
			return nil
		}

		recipes = append(recipes, &Recipe{
			DocxPath: path,
		})

		return nil
	})

	wg := goatomic.WorkerGroup{}
	for _, recipe := range recipes {
		wg.Add(1)

		go func(recipe *Recipe) {
			recipe.ParseFiles()
			wg.Done()
		}(recipe)
	}

	wg.Wait()

	return
}
