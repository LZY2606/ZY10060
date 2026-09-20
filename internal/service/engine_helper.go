package service

import "metrolab/internal/engine"

func runForValidation(spec engine.Spec) (*engine.Output, engine.Problems) {
	return engine.Run(spec, false)
}
