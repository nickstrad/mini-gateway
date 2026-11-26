package pgwire

// PlantSetForwardable enables the test-only canary that treats session SET as
// forwardable so external e2e tests can verify the scenario catches leaks.
func PlantSetForwardable(c *ClassifierConfig) { c.plantSetForwardable = true }
