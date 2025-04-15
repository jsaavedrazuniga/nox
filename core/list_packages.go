package main

import (
	"fmt"
	"log"

	"github.com/lotusdblabs/lotusdb/v2"
)

func main() {
	// Configura las opciones de la base de datos
	options := lotusdb.DefaultOptions
	options.DirPath = "./nox_packages" // Ruta donde está tu base de datos

	// Abre la base de datos
	db, err := lotusdb.Open(options)
	if err != nil {
		log.Fatalf("Error al abrir la base de datos: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("Error al cerrar la base de datos: %v", err)
		}
	}()

	// Crea un iterador para recorrer todas las claves
	iterOpts := lotusdb.IteratorOptions{
		Prefix:  nil,   // Sin prefijo, para obtener todas las claves
		Reverse: false, // Orden ascendente
	}

	iter, err := db.NewIterator(iterOpts)
	if err != nil {
		log.Fatalf("Error al crear el iterador: %v", err)
	}
	defer func() {
		if err := iter.Close(); err != nil {
			log.Printf("Error al cerrar el iterador: %v", err)
		}
	}()

	fmt.Println("Claves existentes en la base de datos:")
	for iter.Rewind(); iter.Valid(); iter.Next() {
		key := iter.Key()
		fmt.Println(string(key))
	}
}
