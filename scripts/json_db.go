package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lotusdblabs/lotusdb/v2"
)

// Estructura para almacenar logs detallados
type ProcessingLog struct {
	FileName       string
	Success        bool
	ErrorMessage   string
	ProcessingTime time.Duration
	Fields         map[string]string // Campos que causaron problemas
}

// Para manejar campos que pueden ser tanto string como array
type StringOrArray []string

func (soa *StringOrArray) UnmarshalJSON(data []byte) error {
	// Verificar si es null
	if string(data) == "null" {
		*soa = []string{}
		return nil
	}

	// Intentar primero como string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*soa = []string{s}
		return nil
	}

	// Si falla, intentar como array
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*soa = arr
		return nil
	}

	// Si ambos fallan, simplemente usar un array vacío
	*soa = []string{}
	return nil
}

// Para manejar campos hash que pueden ser string o array
type FlexibleHash struct {
	IsString bool
	IsArray  bool
	String   string
	Array    []string
}

func (fh *FlexibleHash) UnmarshalJSON(data []byte) error {
	// Verificar si es null
	if string(data) == "null" {
		fh.IsString = true
		fh.String = ""
		return nil
	}

	// Intentar primero como string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		fh.IsString = true
		fh.String = s
		return nil
	}

	// Si falla, intentar como array
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		fh.IsArray = true
		fh.Array = arr
		return nil
	}

	// Si ambos fallan, usar valores por defecto
	fh.IsString = true
	fh.String = ""
	return nil
}

// Para manejar campos que pueden ser string, objeto, booleano, número o null
type FlexibleValue struct {
	IsString bool
	IsObject bool
	IsBool   bool
	IsNumber bool
	IsNull   bool
	String   string
	Object   map[string]interface{}
	Bool     bool
	Number   float64
}

func (fv *FlexibleValue) UnmarshalJSON(data []byte) error {
	// Verificar si es null
	if string(data) == "null" {
		fv.IsNull = true
		return nil
	}

	// Intentar como string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		fv.IsString = true
		fv.String = s
		return nil
	}

	// Intentar como objeto
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err == nil {
		fv.IsObject = true
		fv.Object = obj
		return nil
	}

	// Intentar como booleano
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		fv.IsBool = true
		fv.Bool = b
		return nil
	}

	// Intentar como número
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		fv.IsNumber = true
		fv.Number = n
		return nil
	}

	// Si no coincide con nada, considerar que es null
	fv.IsNull = true
	return nil
}

// ScoopManifest representa la estructura del archivo de manifiesto de Scoop
// con tipos flexibles para manejar diferentes formatos
type ScoopManifest struct {
	Version      string        `json:"version"`
	Description  string        `json:"description"`
	Homepage     string        `json:"homepage"`
	License      FlexibleValue `json:"license"`
	Architecture map[string]struct {
		URL  FlexibleValue `json:"url"`
		Hash FlexibleHash  `json:"hash"` // Actualizado a FlexibleHash
	} `json:"architecture"`
	ExtractDir StringOrArray          `json:"extract_dir"` // Cambiado de string a StringOrArray
	PreInstall StringOrArray          `json:"pre_install"`
	Bin        StringOrArray          `json:"bin"`
	CheckVer   FlexibleValue          `json:"checkver"`
	AutoUpdate map[string]interface{} `json:"autoupdate"`
}

// PackageManifest representa la estructura del archivo package.json
type PackageManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Type        string `json:"type"`

	Platforms map[string]struct {
		Version string `json:"version,omitempty"`
		URL     string `json:"url,omitempty"`
		Hash    struct {
			Type  string `json:"type,omitempty"`
			Value string `json:"value,omitempty"`
		} `json:"hash,omitempty"`
	} `json:"platforms,omitempty"`

	Arch []string `json:"arch,omitempty"`

	Source struct {
		Type     string `json:"type,omitempty"`
		URL      string `json:"url,omitempty"`
		Branch   string `json:"branch,omitempty"`
		Revision string `json:"revision,omitempty"`
	} `json:"source,omitempty"`

	PreInstall struct {
		Commands []string          `json:"commands,omitempty"`
		Env      map[string]string `json:"env,omitempty"`
	} `json:"preinstall,omitempty"`

	Install struct {
		Commands []string          `json:"commands,omitempty"`
		Env      map[string]string `json:"env,omitempty"`
	} `json:"install,omitempty"`

	Uninstall struct {
		Commands []string `json:"commands,omitempty"`
	} `json:"uninstall,omitempty"`

	PostInstall struct {
		Commands []string `json:"commands,omitempty"`
	} `json:"postinstall,omitempty"`

	Meta struct {
		Author   string `json:"author"`
		License  string `json:"license,omitempty"`
		Homepage string `json:"homepage,omitempty"`
	} `json:"meta,omitempty"`
}

// Cache para resultados de procesamiento
type Cache struct {
	mu    sync.RWMutex
	items map[string][]byte // Clave: ruta del archivo, Valor: JSON procesado
}

func NewCache() *Cache {
	return &Cache{
		items: make(map[string][]byte),
	}
}

func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, exists := c.items[key]
	return val, exists
}

func (c *Cache) Set(key string, val []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = val
}

// Variables globales
var (
	cache = NewCache()
	// logMu    sync.Mutex
	procLogs []ProcessingLog
)

func main() {
	startTime := time.Now()

	if len(os.Args) < 2 {
		fmt.Println("Uso: json-db <directorio-con-archivos-scoop> [directorio-lotusdb] [num-workers] [--debug]")
		os.Exit(1)
	}

	scoopDir := os.Args[1]
	dbDir := "lotusdb"
	if len(os.Args) > 2 {
		dbDir = os.Args[2]
	}

	// Número de workers, por defecto usa la mitad de los núcleos disponibles
	numWorkers := runtime.NumCPU() / 2
	if numWorkers < 1 {
		numWorkers = 1
	}
	if len(os.Args) > 3 {
		fmt.Sscanf(os.Args[3], "%d", &numWorkers)
	}

	// Modo debug
	debug := false
	for _, arg := range os.Args {
		if arg == "--debug" {
			debug = true
			break
		}
	}

	// Configurar logger
	logFile, err := os.Create("processing.log")
	if err == nil {
		defer logFile.Close()
		log.SetOutput(logFile)
	}

	// Crear/abrir base de datos LotusDB
	db, err := setupDatabase(dbDir)
	if err != nil {
		fmt.Printf("Error al configurar LotusDB: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Procesar archivos
	files, err := os.ReadDir(scoopDir)
	if err != nil {
		fmt.Printf("Error al leer el directorio: %v\n", err)
		os.Exit(1)
	}

	// Crear canal para archivos a procesar
	filesChan := make(chan string, len(files))
	resultsChan := make(chan ProcessingLog, len(files))

	// Cargar el canal con archivos
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
			filesChan <- filepath.Join(scoopDir, file.Name())
		}
	}
	close(filesChan)

	// Lanzar workers
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(workerId int) {
			defer wg.Done()
			processFiles(filesChan, resultsChan, db, debug, workerId)
		}(i)
	}

	// Recoger resultados en otro goroutine
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Procesar resultados
	successCount := 0
	errorCount := 0
	for result := range resultsChan {
		if result.Success {
			successCount++
			if debug {
				fmt.Printf("Procesado correctamente: %s\n", result.FileName)
			}
		} else {
			errorCount++
			fmt.Printf("Error al procesar %s: %s\n", result.FileName, result.ErrorMessage)
		}
		procLogs = append(procLogs, result)
	}

	// Guardar logs detallados a un archivo JSON
	if debug {
		logsJSON, _ := json.MarshalIndent(procLogs, "", "  ")
		os.WriteFile("processing_details.json", logsJSON, 0644)
	}

	elapsedTime := time.Since(startTime)
	fmt.Printf("\nResumen: %d archivos procesados correctamente, %d errores\n", successCount, errorCount)
	fmt.Printf("Tiempo total: %v, Promedio por archivo: %v\n",
		elapsedTime,
		elapsedTime/time.Duration(successCount+errorCount))
}

func setupDatabase(dbDir string) (*lotusdb.DB, error) {
	// Asegurar que el directorio existe
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, err
	}

	// Opciones de configuración para LotusDB
	options := lotusdb.DefaultOptions
	options.DirPath = dbDir

	// Abrir o crear la base de datos
	db, err := lotusdb.Open(options)
	if err != nil {
		return nil, err
	}

	return db, nil
}

func processFiles(filesChan <-chan string, resultsChan chan<- ProcessingLog, db *lotusdb.DB, debug bool, workerId int) {
	for filePath := range filesChan {
		startTime := time.Now()
		fileName := filepath.Base(filePath)

		// Verificar caché primero
		if cachedData, found := cache.Get(filePath); found {
			// Recuperar del caché - simplemente guardar en la base de datos
			packageName := strings.TrimSuffix(fileName, filepath.Ext(fileName))

			err := db.Put([]byte(packageName), cachedData)
			if err != nil {
				resultsChan <- ProcessingLog{
					FileName:     fileName,
					Success:      false,
					ErrorMessage: fmt.Sprintf("Error al guardar en DB desde caché: %v", err),
				}
				continue
			}

			fileKey := "file:" + fileName
			err = db.Put([]byte(fileKey), []byte(packageName))
			if err != nil {
				resultsChan <- ProcessingLog{
					FileName:     fileName,
					Success:      false,
					ErrorMessage: fmt.Sprintf("Error al guardar referencia en DB desde caché: %v", err),
				}
				continue
			}

			outputPath := strings.TrimSuffix(filePath, ".json") + "_package.json"
			err = os.WriteFile(outputPath, cachedData, 0644)
			if err != nil {
				resultsChan <- ProcessingLog{
					FileName:     fileName,
					Success:      false,
					ErrorMessage: fmt.Sprintf("Error al escribir archivo desde caché: %v", err),
				}
				continue
			}

			resultsChan <- ProcessingLog{
				FileName:       fileName,
				Success:        true,
				ProcessingTime: time.Since(startTime),
			}
			continue
		}

		// No está en caché, procesar normalmente
		pLog := ProcessingLog{
			FileName: fileName,
			Success:  false,
			Fields:   make(map[string]string),
		}

		err := processFile(filePath, db, debug, &pLog)
		pLog.ProcessingTime = time.Since(startTime)

		if err != nil {
			pLog.ErrorMessage = err.Error()
		} else {
			pLog.Success = true
		}

		resultsChan <- pLog
	}
}

func processFile(filePath string, db *lotusdb.DB, debug bool, pLog *ProcessingLog) error {
	// Leer archivo de manifiesto de Scoop
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var scoopManifest ScoopManifest
	if err := json.Unmarshal(data, &scoopManifest); err != nil {
		if debug {
			// Intentar desempaquetar como mapa genérico para ver qué campos son problemáticos
			var rawData map[string]interface{}
			if jsonErr := json.Unmarshal(data, &rawData); jsonErr == nil {
				for k, v := range rawData {
					pLog.Fields[k] = fmt.Sprintf("%T", v)
				}
			}
		}
		return err
	}

	// Obtener nombre del archivo sin extensión para usarlo como nombre del paquete
	fileName := filepath.Base(filePath)
	packageName := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	// Verificar si ya tenemos este paquete en la DB
	_, err = db.Get([]byte(packageName))
	if err == nil {
		// Ya existe, omitir
		pLog.Fields["status"] = "ya existe en DB"
		return nil
	}

	// Convertir a formato package.json
	packageManifest := convertToPackageFormat(packageName, scoopManifest)

	// Serializar a JSON
	packageData, err := json.MarshalIndent(packageManifest, "", "  ")
	if err != nil {
		return err
	}

	// Guardar en caché
	cache.Set(filePath, packageData)

	// Guardar en la base de datos usando el nombre del paquete como clave
	if err := db.Put([]byte(packageName), packageData); err != nil {
		return err
	}

	// También guardar una referencia por nombre de archivo
	fileKey := "file:" + fileName
	if err := db.Put([]byte(fileKey), []byte(packageName)); err != nil {
		return err
	}

	// Opcionalmente, guardar el archivo JSON convertido
	outputPath := strings.TrimSuffix(filePath, ".json") + "_package.json"
	return os.WriteFile(outputPath, packageData, 0644)
}

func convertToPackageFormat(packageName string, scoop ScoopManifest) PackageManifest {
	pkg := PackageManifest{
		Name:        packageName,
		Version:     scoop.Version,
		Description: scoop.Description,
		Type:        "program", // Siempre "program" como se solicitó
		Platforms: make(map[string]struct {
			Version string `json:"version,omitempty"`
			URL     string `json:"url,omitempty"`
			Hash    struct {
				Type  string `json:"type,omitempty"`
				Value string `json:"value,omitempty"`
			} `json:"hash,omitempty"`
		}),
	}

	// Configurar plataformas
	for arch, details := range scoop.Architecture {
		platformKey := "windows"
		if arch == "64bit" {
			platformKey = "windows-x64"
		} else if arch == "32bit" {
			platformKey = "windows-x86"
		}

		platform := struct {
			Version string `json:"version,omitempty"`
			URL     string `json:"url,omitempty"`
			Hash    struct {
				Type  string `json:"type,omitempty"`
				Value string `json:"value,omitempty"`
			} `json:"hash,omitempty"`
		}{
			Version: scoop.Version,
		}

		// Extraer URL, manejando diferentes tipos
		if details.URL.IsString {
			platform.URL = details.URL.String
		} else if details.URL.IsObject {
			// Si es un objeto, intentamos usar el primer valor que encontremos
			for _, v := range details.URL.Object {
				if str, ok := v.(string); ok {
					platform.URL = str
					break
				}
			}
		}

		platform.Hash.Type = "sha256"

		// Manejar hash que puede ser string o array
		if details.Hash.IsString {
			platform.Hash.Value = details.Hash.String
		} else if details.Hash.IsArray && len(details.Hash.Array) > 0 {
			// Si es un array, usamos el primer valor
			platform.Hash.Value = details.Hash.Array[0]
		}

		pkg.Platforms[platformKey] = platform
	}

	// Arquitecturas soportadas
	if len(scoop.Architecture) > 0 {
		for arch := range scoop.Architecture {
			if arch == "64bit" {
				pkg.Arch = append(pkg.Arch, "x64")
			} else if arch == "32bit" {
				pkg.Arch = append(pkg.Arch, "x86")
			}
		}
	}

	// Configurar origen
	if scoop.Homepage != "" {
		pkg.Source.Type = "web"
		pkg.Source.URL = scoop.Homepage
	}

	// Configurar comandos de preinstalación
	if len(scoop.PreInstall) > 0 {
		pkg.PreInstall.Commands = scoop.PreInstall
	}

	// Configurar comandos de instalación
	if len(scoop.Bin) > 0 {
		installCommands := make([]string, 0)
		for _, bin := range scoop.Bin {
			installCommands = append(installCommands, fmt.Sprintf("cp %s $INSTALL_DIR/", bin))
		}
		pkg.Install.Commands = installCommands
	}

	// Metadatos
	pkg.Meta.Author = "Scoop Team" // Valor por defecto según requerimiento

	// Manejar licencia que puede ser string, objeto, booleano, número o null
	if scoop.License.IsString {
		pkg.Meta.License = scoop.License.String
	} else if scoop.License.IsObject {
		// Si es un objeto, intentamos usar el identificador o el primer valor string
		if identifier, ok := scoop.License.Object["identifier"].(string); ok {
			pkg.Meta.License = identifier
		} else {
			// Tomar el primer valor string que encontremos
			for _, v := range scoop.License.Object {
				if str, ok := v.(string); ok {
					pkg.Meta.License = str
					break
				}
			}
		}
	} else if scoop.License.IsBool && scoop.License.Bool {
		pkg.Meta.License = "Unknown" // Si es true, usar "Unknown"
	} else if scoop.License.IsNumber {
		pkg.Meta.License = fmt.Sprintf("%v", scoop.License.Number) // Convertir número a string
	}

	if scoop.Homepage != "" {
		pkg.Meta.Homepage = scoop.Homepage
	}

	return pkg
}
