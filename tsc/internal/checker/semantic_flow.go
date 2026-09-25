package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
)

// SemanticFlowGraph is the syntax-neutral adapter for the checker's existing
// TypeScript control-flow graph. Language frontends describe graph topology and
// resolve their own syntax at operation boundaries; traversal, shared-node
// caching, branch joins, and loop fixed points remain in getTypeAtFlowNode.
type SemanticFlowGraph struct {
	operations  map[*ast.FlowNode]*semanticFlowOperation
	start       *ast.FlowNode
	unreachable *ast.FlowNode
}

// SemanticFlowPoint is an opaque point in a SemanticFlowGraph.
type SemanticFlowPoint struct {
	graph *SemanticFlowGraph
	node  *ast.FlowNode
}

type semanticFlowOperationKind uint8

const (
	semanticFlowAssignment semanticFlowOperationKind = iota
	semanticFlowSnapshot
	semanticFlowCondition
	semanticFlowCall
)

type semanticFlowOperation struct {
	kind         semanticFlowOperationKind
	reference    string
	assignedType *Type
	narrow       func(reference string, source *Type, assumeTrue bool) *Type
	unreachable  bool
}

const mergeFlowReference = "#flow-merge"

func NewSemanticFlowGraph() *SemanticFlowGraph {
	graph := &SemanticFlowGraph{operations: make(map[*ast.FlowNode]*semanticFlowOperation)}
	graph.start = &ast.FlowNode{Flags: ast.FlowFlagsStart}
	graph.unreachable = &ast.FlowNode{Flags: ast.FlowFlagsUnreachable}
	return graph
}

func (g *SemanticFlowGraph) Start() SemanticFlowPoint {
	return SemanticFlowPoint{graph: g, node: g.start}
}

func (g *SemanticFlowGraph) Unreachable() SemanticFlowPoint {
	return SemanticFlowPoint{graph: g, node: g.unreachable}
}

// Assignment creates a flow mutation whose value was resolved by the source
// frontend. Python and TypeScript differ in how assignment syntax is read, but
// subsequent graph traversal is shared.
func (g *SemanticFlowGraph) Assignment(antecedent SemanticFlowPoint, reference string, assignedType *Type) SemanticFlowPoint {
	return g.mutation(antecedent, semanticFlowAssignment, reference, assignedType)
}

// Snapshot records a type state already computed while a frontend checks a
// basic block. It is a migration boundary for statement binders; unlike an
// Assignment it does not re-apply TypeScript's assignment reduction.
func (g *SemanticFlowGraph) Snapshot(antecedent SemanticFlowPoint, reference string, flowType *Type) SemanticFlowPoint {
	return g.mutation(antecedent, semanticFlowSnapshot, reference, flowType)
}

func (g *SemanticFlowGraph) mutation(antecedent SemanticFlowPoint, kind semanticFlowOperationKind, reference string, assignedType *Type) SemanticFlowPoint {
	g.requirePoint(antecedent)
	g.markReferenced(antecedent.node)
	node := &ast.FlowNode{Flags: ast.FlowFlagsAssignment, Antecedent: antecedent.node}
	g.operations[node] = &semanticFlowOperation{kind: kind, reference: reference, assignedType: assignedType}
	return SemanticFlowPoint{graph: g, node: node}
}

// Condition creates one true or false edge. narrow resolves the source
// language's expression and then calls checker-owned narrowing operations.
func (g *SemanticFlowGraph) Condition(antecedent SemanticFlowPoint, assumeTrue bool, narrow func(reference string, source *Type, assumeTrue bool) *Type) SemanticFlowPoint {
	g.requirePoint(antecedent)
	if antecedent.node.Flags&ast.FlowFlagsUnreachable != 0 {
		return antecedent
	}
	g.markReferenced(antecedent.node)
	flags := ast.FlowFlagsFalseCondition
	if assumeTrue {
		flags = ast.FlowFlagsTrueCondition
	}
	node := &ast.FlowNode{Flags: flags, Antecedent: antecedent.node}
	g.operations[node] = &semanticFlowOperation{kind: semanticFlowCondition, narrow: narrow}
	return SemanticFlowPoint{graph: g, node: node}
}

// Call creates the same effects edge used for TypeScript assertion calls and
// never-returning functions. The source frontend supplies only the resolved
// call effect.
func (g *SemanticFlowGraph) Call(antecedent SemanticFlowPoint, narrow func(reference string, source *Type) *Type, unreachable bool) SemanticFlowPoint {
	g.requirePoint(antecedent)
	if antecedent.node.Flags&ast.FlowFlagsUnreachable != 0 {
		return antecedent
	}
	g.markReferenced(antecedent.node)
	node := &ast.FlowNode{Flags: ast.FlowFlagsCall, Antecedent: antecedent.node}
	operation := &semanticFlowOperation{kind: semanticFlowCall, unreachable: unreachable}
	if narrow != nil {
		operation.narrow = func(reference string, source *Type, _ bool) *Type {
			return narrow(reference, source)
		}
	}
	g.operations[node] = operation
	return SemanticFlowPoint{graph: g, node: node}
}

// Branch creates the same non-looping junction used for TypeScript if/else
// and other converging control-flow paths.
func (g *SemanticFlowGraph) Branch(antecedents ...SemanticFlowPoint) SemanticFlowPoint {
	label := &ast.FlowNode{Flags: ast.FlowFlagsBranchLabel}
	for _, antecedent := range antecedents {
		g.addAntecedent(label, antecedent)
	}
	return g.finishLabel(label)
}

// LoopLabel creates an unfinished TypeScript loop junction. AddAntecedent is
// used once for the entry path and again for each back edge.
func (g *SemanticFlowGraph) LoopLabel() SemanticFlowPoint {
	return SemanticFlowPoint{graph: g, node: &ast.FlowNode{Flags: ast.FlowFlagsLoopLabel}}
}

func (g *SemanticFlowGraph) AddAntecedent(label SemanticFlowPoint, antecedent SemanticFlowPoint) {
	g.requirePoint(label)
	if label.node.Flags&ast.FlowFlagsLabel == 0 {
		panic("semantic flow antecedents can only be added to labels")
	}
	g.addAntecedent(label.node, antecedent)
}

func (g *SemanticFlowGraph) FinishLabel(label SemanticFlowPoint) SemanticFlowPoint {
	g.requirePoint(label)
	return g.finishLabel(label.node)
}

func (g *SemanticFlowGraph) addAntecedent(label *ast.FlowNode, antecedent SemanticFlowPoint) {
	g.requirePoint(antecedent)
	if antecedent.node.Flags&ast.FlowFlagsUnreachable != 0 {
		return
	}
	var last *ast.FlowList
	for list := label.Antecedents; list != nil; list = list.Next {
		if list.Flow == antecedent.node {
			return
		}
		last = list
	}
	entry := &ast.FlowList{Flow: antecedent.node}
	if last == nil {
		label.Antecedents = entry
	} else {
		last.Next = entry
	}
	g.markReferenced(antecedent.node)
}

func (g *SemanticFlowGraph) finishLabel(label *ast.FlowNode) SemanticFlowPoint {
	if label.Antecedents == nil {
		return g.Unreachable()
	}
	if label.Antecedents.Next == nil {
		return SemanticFlowPoint{graph: g, node: label.Antecedents.Flow}
	}
	return SemanticFlowPoint{graph: g, node: label}
}

func (g *SemanticFlowGraph) markReferenced(node *ast.FlowNode) {
	if node.Flags&ast.FlowFlagsReferenced == 0 {
		node.Flags |= ast.FlowFlagsReferenced
	} else {
		node.Flags |= ast.FlowFlagsShared
	}
}

func (g *SemanticFlowGraph) requirePoint(point SemanticFlowPoint) {
	if point.graph != g || point.node == nil {
		panic("semantic flow point belongs to a different graph")
	}
}

func (f *FlowState) semanticOperation(flow *ast.FlowNode) *semanticFlowOperation {
	if f.semanticGraph == nil {
		return nil
	}
	return f.semanticGraph.operations[flow]
}

// GetSemanticFlowType evaluates an opaque point by entering the existing
// checker flow traversal. No separate branch, cache, or fixed-point algorithm
// exists in the adapter.
func (c *Checker) GetSemanticFlowType(point SemanticFlowPoint, reference string, declaredType *Type, initialType *Type) *Type {
	if point.graph == nil || point.node == nil {
		return core.Coalesce(initialType, declaredType)
	}
	if declaredType == nil {
		declaredType = c.unknownType
	}
	if initialType == nil {
		initialType = declaredType
	}
	f := c.getFlowState()
	f.semanticGraph = point.graph
	f.semanticRef = reference
	f.declaredType = declaredType
	f.initialType = initialType
	f.sharedFlowStart = len(c.sharedFlows)
	c.flowInvocationCount++
	result := c.getTypeAtFlowNode(f, point.node).t
	c.sharedFlows = c.sharedFlows[:f.sharedFlowStart]
	c.putFlowState(f)
	return c.finalizeEvolvingArrayType(result)
}
