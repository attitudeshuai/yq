package yqlib

import (
	"fmt"
	"log/slog"
)

type DataTreeNavigator interface {
	// given the context and an expressionNode,
	// this will process the against the given expressionNode and return
	// a new context of matching candidates
	GetMatchingNodes(context Context, expressionNode *ExpressionNode) (Context, error)

	DeeplyAssign(context Context, path []interface{}, rhsNode *CandidateNode) error
}

type dataTreeNavigator struct {
	// multiFileBatch records that evaluation was started against a batch of
	// multiple input files, so document frames on the first document render
	// the source filename. It is set by the evaluators in EvaluateFiles and
	// defaults to false so direct/library callers keep the single source
	// behaviour.
	multiFileBatch bool
}

func NewDataTreeNavigator() DataTreeNavigator {
	return &dataTreeNavigator{}
}

func (d *dataTreeNavigator) DeeplyAssign(context Context, path []interface{}, rhsCandidateNode *CandidateNode) error {

	assignmentOp := &Operation{OperationType: assignOpType, Preferences: assignPreferences{}}

	if rhsCandidateNode.Kind == MappingNode {
		log.Debug("DeeplyAssign: deeply merging object")
		// if the rhs is a map, we need to deeply merge it in.
		// otherwise we'll clobber any existing fields
		assignmentOp = &Operation{OperationType: multiplyAssignOpType, Preferences: multiplyPreferences{
			AppendArrays:  true,
			TraversePrefs: traversePreferences{DontFollowAlias: true},
			AssignPrefs:   assignPreferences{},
		}}
	}

	rhsOp := &Operation{OperationType: valueOpType, CandidateNode: rhsCandidateNode}

	assignmentOpNode := &ExpressionNode{
		Operation: assignmentOp,
		LHS:       createTraversalTree(path, traversePreferences{}, false),
		RHS:       &ExpressionNode{Operation: rhsOp},
	}

	_, err := d.GetMatchingNodes(context, assignmentOpNode)
	return err
}

func (d *dataTreeNavigator) GetMatchingNodes(context Context, expressionNode *ExpressionNode) (Context, error) {
	if expressionNode == nil {
		log.Debugf("getMatchingNodes - nothing to do")
		return context, nil
	}
	log.Debugf("Processing Op: %v", expressionNode.Operation.toString())
	if log.IsEnabledFor(slog.LevelDebug) {
		for el := context.MatchingNodes.Front(); el != nil; el = el.Next() {
			log.Debug(NodeToString(el.Value.(*CandidateNode)))
		}
	}
	handler := expressionNode.Operation.OperationType.Handler
	if handler != nil {
		result, err := handler(d, context, expressionNode)
		if err != nil {
			return result, d.attachDocumentError(err, context)
		}
		return result, nil
	}
	return Context{}, fmt.Errorf("unknown operator %v", expressionNode.Operation.OperationType.Type)

}

// attachDocumentError is the single chokepoint where operator failures are
// attributed to their source document. Only an unambiguous context (exactly
// one candidate node) is attributed; ambiguous multi-document contexts keep
// the error untouched. The frame is idempotent per stage, so the layer
// closest to the failure site wins as the error unwinds.
func (d *dataTreeNavigator) attachDocumentError(err error, context Context) error {
	if err == nil || context.MatchingNodes == nil || context.MatchingNodes.Len() != 1 {
		return err
	}
	candidate := context.MatchingNodes.Front().Value.(*CandidateNode)
	return newDocumentErrorForCandidate(err, candidate, d.multiFileBatch)
}
