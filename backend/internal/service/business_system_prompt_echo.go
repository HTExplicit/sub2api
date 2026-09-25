package service

import (
	"fmt"
	"sort"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// redactPromptRuleCarrierEcho is used only after the exact final carrier hash
// matched. Public source blocks remain visible, including a Claude system
// message containing both public and private rule blocks.
func redactPromptRuleCarrierEcho(envelope []byte, proof promptRulesCarrierUndo, application BusinessSystemPromptApplication) ([]byte, error) {
	var placements []extensionv1.PromptRulePlacement
	public := false
	for _, placement := range application.RulesPlan.Placements {
		if placement.Carrier == proof.field {
			placements = append(placements, placement)
			public = public || placement.PreserveEcho
		}
	}
	if !public {
		if proof.restorable {
			return restorePromptRules(envelope, []promptRulesCarrierUndo{proof})
		}
		// A huge original scalar is deliberately not retained. The exact
		// echoed carrier can still be omitted without exposing private text.
		return sjson.DeleteBytes(envelope, proof.field)
	}
	out := envelope
	messagePublic := map[int]bool{}
	for _, placement := range placements {
		if placement.Index != nil && placement.PreserveEcho {
			messagePublic[*placement.Index] = true
		}
	}
	removeItems := map[int]bool{}
	removeBlocks := map[int][]int{}
	for _, placement := range placements {
		if placement.PreserveEcho {
			continue
		}
		if placement.Index == nil {
			return sjson.DeleteBytes(envelope, proof.field)
		}
		index := *placement.Index
		if proof.field == "messages" && placement.Protocol == "messages" && messagePublic[index] && placement.BlockIndex != nil {
			removeBlocks[index] = append(removeBlocks[index], *placement.BlockIndex)
			continue
		}
		count := 1
		if proof.field == "system" && placement.ContentFormat == extensionv1.PromptContentAnthropicSystemBlocks {
			count = len(gjson.ParseBytes(placement.StructuredContent).Array())
		}
		for offset := 0; offset < count; offset++ {
			removeItems[index+offset] = true
		}
	}
	for message, blocks := range removeBlocks {
		sort.Sort(sort.Reverse(sort.IntSlice(blocks)))
		for _, block := range blocks {
			var err error
			out, err = sjson.DeleteBytes(out, fmt.Sprintf("%s.%d.content.%d", proof.field, message, block))
			if err != nil {
				return nil, err
			}
		}
	}
	indices := make([]int, 0, len(removeItems))
	for index := range removeItems {
		indices = append(indices, index)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(indices)))
	arrayPath := proof.field
	if proof.field == "systemInstruction" {
		arrayPath += ".parts"
	}
	for _, index := range indices {
		var err error
		out, err = sjson.DeleteBytes(out, fmt.Sprintf("%s.%d", arrayPath, index))
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
