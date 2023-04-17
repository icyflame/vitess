/*
Copyright 2023 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package evalengine

import (
	"time"

	"vitess.io/vitess/go/hack"
	"vitess.io/vitess/go/mysql/collations"
	"vitess.io/vitess/go/mysql/datetime"
	"vitess.io/vitess/go/sqltypes"
	querypb "vitess.io/vitess/go/vt/proto/query"
)

var SystemTime = time.Now

type (
	builtinNow struct {
		CallExpr
		utc      bool
		onlyTime bool
		prec     uint8
	}

	builtinSysdate struct {
		CallExpr
		prec uint8
	}

	builtinCurdate struct {
		CallExpr
	}

	builtinUtcDate struct {
		CallExpr
	}

	builtinDateFormat struct {
		CallExpr
		collate collations.ID
	}
)

var _ Expr = (*builtinNow)(nil)
var _ Expr = (*builtinSysdate)(nil)
var _ Expr = (*builtinCurdate)(nil)
var _ Expr = (*builtinUtcDate)(nil)
var _ Expr = (*builtinDateFormat)(nil)

func (call *builtinNow) eval(env *ExpressionEnv) (eval, error) {
	now := env.time(call.utc)
	if call.onlyTime {
		buf := datetime.Time_hh_mm_ss.Format(now, call.prec)
		return newEvalRaw(sqltypes.Time, buf, collationBinary), nil
	} else {
		buf := datetime.DateTime_YYYY_MM_DD_hh_mm_ss.Format(now, call.prec)
		return newEvalRaw(sqltypes.Datetime, buf, collationBinary), nil
	}
}

func (call *builtinNow) typeof(_ *ExpressionEnv, _ []*querypb.Field) (sqltypes.Type, typeFlag) {
	if call.onlyTime {
		return sqltypes.Time, 0
	}
	return sqltypes.Datetime, 0
}

func (call *builtinNow) constant() bool {
	return false
}

func (call *builtinSysdate) eval(env *ExpressionEnv) (eval, error) {
	now := SystemTime()
	if tz := env.currentTimezone(); tz != nil {
		now = now.In(tz)
	}
	return newEvalRaw(sqltypes.Datetime, datetime.FromStdTime(now).Format(call.prec), collationBinary), nil
}

func (call *builtinSysdate) typeof(_ *ExpressionEnv, _ []*querypb.Field) (sqltypes.Type, typeFlag) {
	return sqltypes.Datetime, 0
}

func (call *builtinSysdate) constant() bool {
	return false
}

func (call *builtinCurdate) eval(env *ExpressionEnv) (eval, error) {
	now := env.time(false)
	return newEvalRaw(sqltypes.Date, datetime.Date_YYYY_MM_DD.Format(now, 0), collationBinary), nil
}

func (call *builtinCurdate) typeof(_ *ExpressionEnv, _ []*querypb.Field) (sqltypes.Type, typeFlag) {
	return sqltypes.Date, 0
}

func (call *builtinCurdate) constant() bool {
	return false
}

func (call *builtinUtcDate) eval(env *ExpressionEnv) (eval, error) {
	now := env.time(true)
	return newEvalRaw(sqltypes.Date, datetime.Date_YYYY_MM_DD.Format(now, 0), collationBinary), nil
}

func (call *builtinUtcDate) typeof(_ *ExpressionEnv, _ []*querypb.Field) (sqltypes.Type, typeFlag) {
	return sqltypes.Date, 0
}

func (call *builtinUtcDate) constant() bool {
	return false
}

func (b *builtinDateFormat) eval(env *ExpressionEnv) (eval, error) {
	date, format, err := b.arg2(env)
	if err != nil {
		return nil, err
	}
	if date == nil || format == nil {
		return nil, nil
	}
	var t *evalTemporal
	switch e := date.(type) {
	case *evalTemporal:
		t = e.toDateTime()
	default:
		t = evalToDateTime(date)
		if t == nil || t.isZero() {
			return nil, nil
		}
	}

	f := evalToBinary(format)
	d, err := datetime.Format(f.string(), t.dt, datetime.DefaultPrecision)
	if err != nil {
		return nil, err
	}
	return newEvalText(d, defaultCoercionCollation(b.collate)), nil
}

func (b *builtinDateFormat) typeof(env *ExpressionEnv, fields []*querypb.Field) (sqltypes.Type, typeFlag) {
	return sqltypes.VarChar, flagNullable
}

type builtinConvertTz struct {
	CallExpr
}

var _ Expr = (*builtinConvertTz)(nil)

func convertTz(dt datetime.DateTime, from, to *time.Location) (datetime.DateTime, bool) {
	buf := datetime.DateTime_YYYY_MM_DD_hh_mm_ss.Format(dt, datetime.DefaultPrecision)
	ts, err := time.ParseInLocation(time.DateTime, hack.String(buf), from)
	if err != nil {
		return datetime.DateTime{}, false
	}
	return datetime.FromStdTime(ts.In(to)), true
}

func (call *builtinConvertTz) eval(env *ExpressionEnv) (eval, error) {
	n, err := call.Arguments[0].eval(env)
	if err != nil {
		return nil, err
	}
	from, err := call.Arguments[1].eval(env)
	if err != nil {
		return nil, err
	}
	to, err := call.Arguments[2].eval(env)
	if err != nil {
		return nil, err
	}

	if n == nil || from == nil || to == nil {
		return nil, nil
	}

	f := evalToBinary(from)
	t := evalToBinary(to)

	fromTz, err := datetime.ParseTimeZone(f.string())
	if err != nil {
		return nil, nil
	}

	toTz, err := datetime.ParseTimeZone(t.string())
	if err != nil {
		return nil, nil
	}

	dt := evalToDateTime(n)
	if dt == nil || dt.isZero() {
		return nil, nil
	}

	out, ok := convertTz(dt.dt, fromTz, toTz)
	if !ok {
		return nil, nil
	}
	return newEvalDateTime(out), nil
}

func (call *builtinConvertTz) typeof(env *ExpressionEnv, fields []*querypb.Field) (sqltypes.Type, typeFlag) {
	_, f := call.Arguments[0].typeof(env, fields)
	return sqltypes.Datetime, f | flagNullable
}