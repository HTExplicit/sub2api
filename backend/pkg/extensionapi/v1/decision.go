package extensionv1

import (
	"errors"
	"strconv"
)

// DecisionTable is a bounded ordered list of conjunctions. It has no loops,
// code execution, IO, or implicit inputs; domains supply their own rules.
type DecisionTable struct {
	Rules   []DecisionRule `json:"rules"`
	Default string         `json:"default"`
}
type DecisionRule struct {
	When   []DecisionCondition `json:"when"`
	Result string              `json:"result"`
}
type DecisionCondition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

func (t DecisionTable) Validate() error {
	if len(t.Rules) > 64 || t.Default == "" || len(t.Default) > 64 {
		return errors.New("invalid decision table")
	}
	for _, rule := range t.Rules {
		if len(rule.When) == 0 || len(rule.When) > 16 || rule.Result == "" || len(rule.Result) > 64 {
			return errors.New("invalid decision rule")
		}
		for _, condition := range rule.When {
			if condition.Field == "" || len(condition.Field) > 64 || len(condition.Value) > 256 {
				return errors.New("invalid decision condition")
			}
			switch condition.Operator {
			case "eq", "ne":
			case "gte", "lt":
				if _, err := strconv.ParseInt(condition.Value, 10, 64); err != nil {
					return errors.New("invalid decision number")
				}
			default:
				return errors.New("unknown decision operator")
			}
		}
	}
	return nil
}

func (t DecisionTable) Evaluate(facts map[string]string) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	for _, rule := range t.Rules {
		matched := true
		for _, condition := range rule.When {
			actual, exists := facts[condition.Field]
			if !exists {
				matched = false
				break
			}
			switch condition.Operator {
			case "eq":
				matched = actual == condition.Value
			case "ne":
				matched = actual != condition.Value
			case "gte", "lt":
				number, err := strconv.ParseInt(actual, 10, 64)
				wanted, _ := strconv.ParseInt(condition.Value, 10, 64)
				matched = err == nil && ((condition.Operator == "gte" && number >= wanted) || (condition.Operator == "lt" && number < wanted))
			}
			if !matched {
				break
			}
		}
		if matched {
			return rule.Result, nil
		}
	}
	return t.Default, nil
}
