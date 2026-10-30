package yqlib

import (
	"os"
)

type writeInPlaceHandler interface {
	CreateTempFile() (*os.File, error)
	FinishWriteInPlace(evaluatedSuccessfully bool) error
}

type writeInPlaceHandlerImpl struct {
	inputFilename string
	tempFile      *os.File
	tempEntry     *registryEntry
}

func NewWriteInPlaceHandler(inputFile string) writeInPlaceHandler {

	return &writeInPlaceHandlerImpl{inputFilename: inputFile}
}

func (w *writeInPlaceHandlerImpl) CreateTempFile() (*os.File, error) {
	file, err := createTempFile()

	if err != nil {
		return nil, err
	}
	// Register before the permission/ownership steps so a failure there
	// still leaves the temp file pending on the registry for cleanup.
	w.tempEntry = ActiveRun().registerRename(file.Name(), file, w.inputFilename)

	info, err := os.Stat(w.inputFilename)
	if err != nil {
		return nil, err
	}
	err = os.Chmod(file.Name(), info.Mode())

	if err != nil {
		return nil, err
	}

	if err = changeOwner(info, file); err != nil {
		return nil, err
	}
	log.Debugf("WriteInPlaceHandler: writing to tempfile: %v", file.Name())
	w.tempFile = file
	return file, err
}

func (w *writeInPlaceHandlerImpl) FinishWriteInPlace(evaluatedSuccessfully bool) error {
	log.Debugf("Going to write in place, evaluatedSuccessfully=%v, target=%v", evaluatedSuccessfully, w.inputFilename)
	if w.tempEntry == nil {
		w.tempEntry = ActiveRun().registerRename(w.tempFile.Name(), w.tempFile, w.inputFilename)
	}
	if evaluatedSuccessfully {
		log.Debug("Moving temp file to target")
		return w.tempEntry.Commit()
	}
	w.tempEntry.Rollback()
	return nil
}
