// Copyright 2018 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package colexec

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/col/coldata"
	"github.com/cockroachdb/cockroach/pkg/col/coldatatestutils"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/sql/colexec/execgen"
	"github.com/cockroachdb/cockroach/pkg/sql/colexecbase"
	"github.com/cockroachdb/cockroach/pkg/sql/execinfra"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/sqlbase"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjPlusInt64Int64ConstOp(t *testing.T) {
	defer leaktest.AfterTest(t)()
	ctx := context.Background()
	st := cluster.MakeTestingClusterSettings()
	evalCtx := tree.MakeTestingEvalContext(st)
	defer evalCtx.Stop(ctx)
	flowCtx := &execinfra.FlowCtx{
		EvalCtx: &evalCtx,
		Cfg: &execinfra.ServerConfig{
			Settings: st,
		},
	}
	runTests(t, []tuples{{{1}, {2}, {nil}}}, tuples{{1, 2}, {2, 3}, {nil, nil}}, orderedVerifier,
		func(input []colexecbase.Operator) (colexecbase.Operator, error) {
			return createTestProjectingOperator(
				ctx, flowCtx, input[0], []*types.T{types.Int},
				"@1 + 1" /* projectingExpr */, false, /* canFallbackToRowexec */
			)
		})
}

func TestProjPlusInt64Int64Op(t *testing.T) {
	defer leaktest.AfterTest(t)()
	ctx := context.Background()
	st := cluster.MakeTestingClusterSettings()
	evalCtx := tree.MakeTestingEvalContext(st)
	defer evalCtx.Stop(ctx)
	flowCtx := &execinfra.FlowCtx{
		EvalCtx: &evalCtx,
		Cfg: &execinfra.ServerConfig{
			Settings: st,
		},
	}
	runTests(t, []tuples{{{1, 2}, {3, 4}, {5, nil}}}, tuples{{1, 2, 3}, {3, 4, 7}, {5, nil, nil}},
		orderedVerifier,
		func(input []colexecbase.Operator) (colexecbase.Operator, error) {
			return createTestProjectingOperator(
				ctx, flowCtx, input[0], []*types.T{types.Int, types.Int},
				"@1 + @2" /* projectingExpr */, false, /* canFallbackToRowexec */
			)
		})
}

func TestProjDivFloat64Float64Op(t *testing.T) {
	defer leaktest.AfterTest(t)()
	ctx := context.Background()
	st := cluster.MakeTestingClusterSettings()
	evalCtx := tree.MakeTestingEvalContext(st)
	defer evalCtx.Stop(ctx)
	flowCtx := &execinfra.FlowCtx{
		EvalCtx: &evalCtx,
		Cfg: &execinfra.ServerConfig{
			Settings: st,
		},
	}
	runTests(t, []tuples{{{1.0, 2.0}, {3.0, 4.0}, {5.0, nil}}}, tuples{{1.0, 2.0, 0.5}, {3.0, 4.0, 0.75}, {5.0, nil, nil}},
		orderedVerifier,
		func(input []colexecbase.Operator) (colexecbase.Operator, error) {
			return createTestProjectingOperator(
				ctx, flowCtx, input[0], []*types.T{types.Float, types.Float},
				"@1 / @2" /* projectingExpr */, false, /* canFallbackToRowexec */
			)
		})
}

func TestGetProjectionConstOperator(t *testing.T) {
	defer leaktest.AfterTest(t)()
	binOp := tree.Mult
	var input colexecbase.Operator
	colIdx := 3
	constVal := 31.37
	constArg := tree.NewDFloat(tree.DFloat(constVal))
	outputIdx := 5
	op, err := GetProjectionRConstOperator(
		testAllocator, types.Float, types.Float, types.Float, binOp, input,
		colIdx, constArg, outputIdx, nil /* binFn */, nil, /* evalCtx */
	)
	if err != nil {
		t.Error(err)
	}
	expected := &projMultFloat64Float64ConstOp{
		projConstOpBase: projConstOpBase{
			OneInputNode: NewOneInputNode(op.(*projMultFloat64Float64ConstOp).input),
			allocator:    testAllocator,
			colIdx:       colIdx,
			outputIdx:    outputIdx,
		},
		constArg: constVal,
	}
	if !reflect.DeepEqual(op, expected) {
		t.Errorf("got %+v,\nexpected %+v", op, expected)
	}
}

func TestGetProjectionConstMixedTypeOperator(t *testing.T) {
	defer leaktest.AfterTest(t)()
	binOp := tree.GE
	var input colexecbase.Operator
	colIdx := 3
	constVal := int16(31)
	constArg := tree.NewDInt(tree.DInt(constVal))
	outputIdx := 5
	op, err := GetProjectionRConstOperator(
		testAllocator, types.Int, types.Int2, types.Int, binOp, input, colIdx,
		constArg, outputIdx, nil /* binFn */, nil, /* evalCtx */
	)
	if err != nil {
		t.Error(err)
	}
	expected := &projGEInt64Int16ConstOp{
		projConstOpBase: projConstOpBase{
			OneInputNode: NewOneInputNode(op.(*projGEInt64Int16ConstOp).input),
			allocator:    testAllocator,
			colIdx:       colIdx,
			outputIdx:    outputIdx,
		},
		constArg: constVal,
	}
	if !reflect.DeepEqual(op, expected) {
		t.Errorf("got %+v,\nexpected %+v", op, expected)
	}
}

// TestRandomComparisons runs comparisons against all scalar types with random
// non-null data verifying that the result of Datum.Compare matches the result
// of the exec projection.
func TestRandomComparisons(t *testing.T) {
	defer leaktest.AfterTest(t)()
	ctx := context.Background()
	st := cluster.MakeTestingClusterSettings()
	evalCtx := tree.MakeTestingEvalContext(st)
	defer evalCtx.Stop(ctx)
	flowCtx := &execinfra.FlowCtx{
		EvalCtx: &evalCtx,
		Cfg: &execinfra.ServerConfig{
			Settings: st,
		},
	}
	const numTuples = 2048
	rng, _ := randutil.NewPseudoRand()

	expected := make([]bool, numTuples)
	var da sqlbase.DatumAlloc
	lDatums := make([]tree.Datum, numTuples)
	rDatums := make([]tree.Datum, numTuples)
	for _, typ := range types.Scalar {
		if typ.Family() == types.DateFamily {
			// TODO(jordan): #40354 tracks failure to compare infinite dates.
			continue
		}
		typs := []*types.T{typ, typ, types.Bool}
		bytesFixedLength := 0
		if typ.Family() == types.UuidFamily {
			bytesFixedLength = 16
		}
		b := testAllocator.NewMemBatchWithSize(typs, numTuples)
		lVec := b.ColVec(0)
		rVec := b.ColVec(1)
		ret := b.ColVec(2)
		for _, vec := range []coldata.Vec{lVec, rVec} {
			coldatatestutils.RandomVec(
				coldatatestutils.RandomVecArgs{
					Rand:             rng,
					Vec:              vec,
					N:                numTuples,
					NullProbability:  0,
					BytesFixedLength: bytesFixedLength,
				},
			)
		}
		PhysicalTypeColVecToDatum(lDatums, lVec, numTuples, nil /* sel */, &da)
		PhysicalTypeColVecToDatum(rDatums, rVec, numTuples, nil /* sel */, &da)
		supportedCmpOps := []tree.ComparisonOperator{tree.EQ, tree.NE, tree.LT, tree.LE, tree.GT, tree.GE}
		if typ.Family() == types.JsonFamily {
			supportedCmpOps = []tree.ComparisonOperator{tree.EQ, tree.NE}
		}
		for _, cmpOp := range supportedCmpOps {
			for i := range lDatums {
				cmp := lDatums[i].Compare(&evalCtx, rDatums[i])
				var b bool
				switch cmpOp {
				case tree.EQ:
					b = cmp == 0
				case tree.NE:
					b = cmp != 0
				case tree.LT:
					b = cmp < 0
				case tree.LE:
					b = cmp <= 0
				case tree.GT:
					b = cmp > 0
				case tree.GE:
					b = cmp >= 0
				}
				expected[i] = b
			}
			input := newChunkingBatchSource(typs, []coldata.Vec{lVec, rVec, ret}, numTuples)
			op, err := createTestProjectingOperator(
				ctx, flowCtx, input, []*types.T{typ, typ},
				fmt.Sprintf("@1 %s @2", cmpOp), false, /* canFallbackToRowexec */
			)
			require.NoError(t, err)
			op.Init()
			var idx int
			for batch := op.Next(ctx); batch.Length() > 0; batch = op.Next(ctx) {
				for i := 0; i < batch.Length(); i++ {
					absIdx := idx + i
					assert.Equal(t, expected[absIdx], batch.ColVec(2).Bool()[i],
						"expected %s %s %s (%s[%d]) to be %t found %t", lDatums[absIdx], cmpOp, rDatums[absIdx], typ, absIdx,
						expected[absIdx], ret.Bool()[i])
				}
				idx += batch.Length()
			}
		}
	}
}

func TestGetProjectionOperator(t *testing.T) {
	defer leaktest.AfterTest(t)()
	typ := types.Int2
	binOp := tree.Mult
	var input colexecbase.Operator
	col1Idx := 5
	col2Idx := 7
	outputIdx := 9
	op, err := GetProjectionOperator(
		testAllocator, typ, typ, types.Int2, binOp, input, col1Idx, col2Idx,
		outputIdx, nil /* binFn */, nil, /* evalCtx */
	)
	if err != nil {
		t.Error(err)
	}
	expected := &projMultInt16Int16Op{
		projOpBase: projOpBase{
			OneInputNode: NewOneInputNode(op.(*projMultInt16Int16Op).input),
			allocator:    testAllocator,
			col1Idx:      col1Idx,
			col2Idx:      col2Idx,
			outputIdx:    outputIdx,
		},
	}
	if !reflect.DeepEqual(op, expected) {
		t.Errorf("got %+v,\nexpected %+v", op, expected)
	}
}

func benchmarkProjOp(
	b *testing.B,
	name string,
	makeProjOp func(source *colexecbase.RepeatableBatchSource, left, right *types.T) (colexecbase.Operator, error),
	useSelectionVector bool,
	hasNulls bool,
	rightConst bool,
) {
	ctx := context.Background()
	rng, _ := randutil.NewPseudoRand()

	typs := []*types.T{types.Int, types.Int}
	if rightConst {
		typs = typs[:1]
	}
	batch := testAllocator.NewMemBatch(typs)
	nullProb := 0.0
	if hasNulls {
		nullProb = nullProbability
	}
	for _, colVec := range batch.ColVecs() {
		coldatatestutils.RandomVec(coldatatestutils.RandomVecArgs{
			Rand:            rng,
			Vec:             colVec,
			N:               coldata.BatchSize(),
			NullProbability: nullProb,
			// We will limit the range of integers so that we won't get "out of
			// range" errors.
			IntRange: 64,
			// We prohibit zeroes because we might be performing a division.
			ZeroProhibited: true,
		})
	}
	batch.SetLength(coldata.BatchSize())
	if useSelectionVector {
		batch.SetSelection(true)
		sel := batch.Selection()
		for i := 0; i < coldata.BatchSize(); i++ {
			sel[i] = i
		}
	}
	source := colexecbase.NewRepeatableBatchSource(testAllocator, batch, typs)
	op, err := makeProjOp(source, types.Int, types.Int)
	require.NoError(b, err)
	op.Init()

	b.Run(name, func(b *testing.B) {
		b.SetBytes(int64(len(typs) * 8 * coldata.BatchSize()))
		for i := 0; i < b.N; i++ {
			op.Next(ctx)
		}
	})
}

func BenchmarkProjOp(b *testing.B) {
	if testing.Short() {
		b.Skip()
	}
	ctx := context.Background()
	st := cluster.MakeTestingClusterSettings()
	evalCtx := tree.MakeTestingEvalContext(st)
	defer evalCtx.Stop(ctx)
	flowCtx := &execinfra.FlowCtx{
		EvalCtx: &evalCtx,
		Cfg: &execinfra.ServerConfig{
			Settings: st,
		},
	}

	var (
		opNames []string
		opInfix []string
	)
	for _, binOp := range []tree.BinaryOperator{tree.Plus, tree.Minus, tree.Mult, tree.Div} {
		opNames = append(opNames, execgen.BinaryOpName[binOp])
		opInfix = append(opInfix, binOp.String())
	}
	for _, cmpOp := range []tree.ComparisonOperator{tree.EQ, tree.NE, tree.LT, tree.LE, tree.GT, tree.GE} {
		opNames = append(opNames, execgen.ComparisonOpName[cmpOp])
		opInfix = append(opInfix, cmpOp.String())
	}
	for opIdx, opName := range opNames {
		opInfixForm := opInfix[opIdx]
		for _, useSel := range []bool{false, true} {
			for _, hasNulls := range []bool{false, true} {
				for _, rightConst := range []bool{false, true} {
					kind := ""
					if rightConst {
						kind = "Const"
					}
					name := fmt.Sprintf("proj%sInt64Int64%s/useSel=%t/hasNulls=%t", opName, kind, useSel, hasNulls)
					benchmarkProjOp(b, name, func(source *colexecbase.RepeatableBatchSource, left, right *types.T) (colexecbase.Operator, error) {
						expr := fmt.Sprintf("@1 %s @2", opInfixForm)
						typs := []*types.T{left, right}
						if rightConst {
							expr = fmt.Sprintf("@1 %s 2", opInfixForm)
							typs = typs[:1]
						}
						return createTestProjectingOperator(
							ctx, flowCtx, source, typs, expr, false, /* canFallbackToRowexec */
						)
					}, useSel, hasNulls, rightConst)
				}
			}
		}
	}
}
