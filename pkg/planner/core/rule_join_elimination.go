// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package core

import (
	"bytes"
	"context"
	"fmt"

	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/expression"
	"github.com/pingcap/tidb/pkg/planner/core/base"
	"github.com/pingcap/tidb/pkg/planner/core/operator/logicalop"
	ruleutil "github.com/pingcap/tidb/pkg/planner/core/rule/util"
	"github.com/pingcap/tidb/pkg/planner/util/optimizetrace"
	"github.com/pingcap/tidb/pkg/util/intset"
)

// OuterJoinEliminator is used to eliminate outer join.
type OuterJoinEliminator struct {
}

// tryToEliminateOuterJoin will eliminate outer join plan base on the following rules
//  1. outer join elimination: For example left outer join, if the parent doesn't use the
//     columns from right table and the join key of right table(the inner table) is a unique
//     key of the right table. the left outer join can be eliminated.
//  2. outer join elimination with duplicate agnostic aggregate functions: For example left outer join.
//     If the parent only use the columns from left table with 'distinct' label. The left outer join can
//     be eliminated.
func (o *OuterJoinEliminator) tryToEliminateOuterJoin(p *logicalop.LogicalJoin, aggCols []*expression.Column, parentCols []*expression.Column, opt *optimizetrace.LogicalOptimizeOp) (base.LogicalPlan, bool, error) {
	var innerChildIdx int
	switch p.JoinType {
	case logicalop.LeftOuterJoin:
		innerChildIdx = 1
	case logicalop.RightOuterJoin:
		innerChildIdx = 0
	default:
		return p, false, nil
	}

	outerPlan := p.Children()[1^innerChildIdx]
	innerPlan := p.Children()[innerChildIdx]

	// in case of count(*) FROM R LOJ S, the parentCols is empty, but
	// still need to proceed to check whether we can eliminate outer join.
	// In fact, we only care about whether there is any column from inner
	// table, if there is none, we are good.
	if len(parentCols) > 0 {
		outerUniqueIDs := intset.NewFastIntSet()
		for _, outerCol := range outerPlan.Schema().Columns {
			outerUniqueIDs.Insert(int(outerCol.UniqueID))
		}
		matched := ruleutil.IsColsAllFromOuterTable(parentCols, &outerUniqueIDs)
		if !matched {
			return p, false, nil
		}
	}

	if len(aggCols) > 0 {
		innerUniqueIDs := intset.NewFastIntSet()
		for _, innerCol := range innerPlan.Schema().Columns {
			innerUniqueIDs.Insert(int(innerCol.UniqueID))
		}
		// Check if any column is from the inner table.
		// If any column is from the inner table, we cannot eliminate the outer join.
		innerFound := ruleutil.IsColFromInnerTable(aggCols, &innerUniqueIDs)
		if !innerFound {
			appendOuterJoinEliminateAggregationTraceStep(p, outerPlan, aggCols, opt)
			return outerPlan, true, nil
		}
	}
	// outer join elimination without duplicate agnostic aggregate functions
	innerJoinKeys := o.extractInnerJoinKeys(p, innerChildIdx)
	contain, err := o.isInnerJoinKeysContainUniqueKey(innerPlan, innerJoinKeys)
	if err != nil {
		return p, false, err
	}
	if contain {
		appendOuterJoinEliminateTraceStep(p, outerPlan, parentCols, innerJoinKeys, opt)
		return outerPlan, true, nil
	}
	contain, err = o.isInnerJoinKeysContainIndex(innerPlan, innerJoinKeys)
	if err != nil {
		return p, false, err
	}
	if contain {
		appendOuterJoinEliminateTraceStep(p, outerPlan, parentCols, innerJoinKeys, opt)
		return outerPlan, true, nil
	}

	return p, false, nil
}

// extract join keys as a schema for inner child of a outer join
func (*OuterJoinEliminator) extractInnerJoinKeys(join *logicalop.LogicalJoin, innerChildIdx int) *expression.Schema {
	joinKeys := make([]*expression.Column, 0, len(join.EqualConditions))
	for _, eqCond := range join.EqualConditions {
		joinKeys = append(joinKeys, eqCond.GetArgs()[innerChildIdx].(*expression.Column))
	}
	return expression.NewSchema(joinKeys...)
}

// check whether one of unique keys sets is contained by inner join keys
func (*OuterJoinEliminator) isInnerJoinKeysContainUniqueKey(innerPlan base.LogicalPlan, joinKeys *expression.Schema) (bool, error) {
	for _, keyInfo := range innerPlan.Schema().PKOrUK {
		joinKeysContainKeyInfo := true
		for _, col := range keyInfo {
			if !joinKeys.Contains(col) {
				joinKeysContainKeyInfo = false
				break
			}
		}
		if joinKeysContainKeyInfo {
			return true, nil
		}
	}
	return false, nil
}

// check whether one of index sets is contained by inner join index
func (*OuterJoinEliminator) isInnerJoinKeysContainIndex(innerPlan base.LogicalPlan, joinKeys *expression.Schema) (bool, error) {
	ds, ok := innerPlan.(*logicalop.DataSource)
	if !ok {
		return false, nil
	}
	for _, path := range ds.AllPossibleAccessPaths {
		if path.IsIntHandlePath || !path.Index.Unique || len(path.IdxCols) == 0 {
			continue
		}
		joinKeysContainIndex := true
		for _, idxCol := range path.IdxCols {
			if !joinKeys.Contains(idxCol) {
				joinKeysContainIndex = false
				break
			}
		}
		if joinKeysContainIndex {
			return true, nil
		}
	}
	return false, nil
}

func (o *OuterJoinEliminator) doOptimize(p base.LogicalPlan, aggCols []*expression.Column, parentCols []*expression.Column, opt *optimizetrace.LogicalOptimizeOp) (base.LogicalPlan, error) {
	// CTE's logical optimization is independent.
	if _, ok := p.(*logicalop.LogicalCTE); ok {
		return p, nil
	}
	var err error
	var isEliminated bool
	for join, isJoin := p.(*logicalop.LogicalJoin); isJoin; join, isJoin = p.(*logicalop.LogicalJoin) {
		p, isEliminated, err = o.tryToEliminateOuterJoin(join, aggCols, parentCols, opt)
		if err != nil {
			return p, err
		}
		if !isEliminated {
			break
		}
	}

	switch x := p.(type) {
	case *logicalop.LogicalProjection:
		parentCols = parentCols[:0]
		for _, expr := range x.Exprs {
			parentCols = append(parentCols, expression.ExtractColumns(expr)...)
		}
	case *logicalop.LogicalAggregation:
		parentCols = parentCols[:0]
		for _, groupByItem := range x.GroupByItems {
			parentCols = append(parentCols, expression.ExtractColumns(groupByItem)...)
		}
		for _, aggDesc := range x.AggFuncs {
			for _, expr := range aggDesc.Args {
				parentCols = append(parentCols, expression.ExtractColumns(expr)...)
			}
			for _, byItem := range aggDesc.OrderByItems {
				parentCols = append(parentCols, expression.ExtractColumns(byItem.Expr)...)
			}
		}
	default:
		parentCols = append(parentCols[:0], p.Schema().Columns...)
	}

	if ok, newCols := logicalop.GetDupAgnosticAggCols(p, aggCols); ok {
		aggCols = newCols
	}

	for i, child := range p.Children() {
		newChild, err := o.doOptimize(child, aggCols, parentCols, opt)
		if err != nil {
			return nil, err
		}
		p.SetChild(i, newChild)
	}
	return p, nil
}

// Optimize implements base.LogicalOptRule.<0th> interface.
func (o *OuterJoinEliminator) Optimize(_ context.Context, p base.LogicalPlan, opt *optimizetrace.LogicalOptimizeOp) (base.LogicalPlan, bool, error) {
	planChanged := false
	p, err := o.doOptimize(p, nil, nil, opt)
	return p, planChanged, err
}

// Name implements base.LogicalOptRule.<1st> interface.
func (*OuterJoinEliminator) Name() string {
	return "outer_join_eliminate"
}

func appendOuterJoinEliminateTraceStep(join *logicalop.LogicalJoin, outerPlan base.LogicalPlan, parentCols []*expression.Column,
	innerJoinKeys *expression.Schema, opt *optimizetrace.LogicalOptimizeOp) {
	ectx := join.SCtx().GetExprCtx().GetEvalCtx()
	reason := func() string {
		buffer := bytes.NewBufferString("The columns[")
		for i, col := range parentCols {
			if i > 0 {
				buffer.WriteString(",")
			}
			buffer.WriteString(col.StringWithCtx(ectx, errors.RedactLogDisable))
		}
		buffer.WriteString("] are from outer table, and the inner join keys[")
		for i, key := range innerJoinKeys.Columns {
			if i > 0 {
				buffer.WriteString(",")
			}
			buffer.WriteString(key.StringWithCtx(ectx, errors.RedactLogDisable))
		}
		buffer.WriteString("] are unique")
		return buffer.String()
	}
	action := func() string {
		return fmt.Sprintf("Outer %v_%v is eliminated and become %v_%v", join.TP(), join.ID(), outerPlan.TP(), outerPlan.ID())
	}
	opt.AppendStepToCurrent(join.ID(), join.TP(), reason, action)
}

func appendOuterJoinEliminateAggregationTraceStep(join *logicalop.LogicalJoin, outerPlan base.LogicalPlan, aggCols []*expression.Column, opt *optimizetrace.LogicalOptimizeOp) {
	ectx := join.SCtx().GetExprCtx().GetEvalCtx()
	reason := func() string {
		buffer := bytes.NewBufferString("The columns[")
		for i, col := range aggCols {
			if i > 0 {
				buffer.WriteString(",")
			}
			buffer.WriteString(col.StringWithCtx(ectx, errors.RedactLogDisable))
		}
		buffer.WriteString("] in agg are from outer table, and the agg functions are duplicate agnostic")
		return buffer.String()
	}
	action := func() string {
		return fmt.Sprintf("Outer %v_%v is eliminated and become %v_%v", join.TP(), join.ID(), outerPlan.TP(), outerPlan.ID())
	}
	opt.AppendStepToCurrent(join.ID(), join.TP(), reason, action)
}
