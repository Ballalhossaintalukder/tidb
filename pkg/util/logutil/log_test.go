// Copyright 2017 PingCAP, Inc.
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

package logutil

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/google/uuid"
	"github.com/pingcap/log"
	"github.com/pingcap/tidb/pkg/util/tracing"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestFieldsFromTraceInfo(t *testing.T) {
	fields := fieldsFromTraceInfo(nil)
	require.Equal(t, 0, len(fields))

	fields = fieldsFromTraceInfo(&tracing.TraceInfo{})
	require.Equal(t, 0, len(fields))

	fields = fieldsFromTraceInfo(&tracing.TraceInfo{ConnectionID: 1})
	require.Equal(t, []zap.Field{zap.Uint64("conn", 1)}, fields)

	fields = fieldsFromTraceInfo(&tracing.TraceInfo{SessionAlias: "alias123"})
	require.Equal(t, []zap.Field{zap.String("session_alias", "alias123")}, fields)

	fields = fieldsFromTraceInfo(&tracing.TraceInfo{ConnectionID: 1, SessionAlias: "alias123"})
	require.Equal(t, []zap.Field{zap.Uint64("conn", 1), zap.String("session_alias", "alias123")}, fields)
}

func TestZapLoggerWithKeys(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Skip this test on windows for two reason:
		// 1. The pattern match fails somehow. It seems windows treat \n as slash and character n.
		// 2. Remove file doesn't work as long as the log instance hold the file.
		t.Skip("skip on windows")
	}

	fileCfg := FileLogConfig{log.FileLogConfig{Filename: fmt.Sprintf("zap_log_%s", uuid.NewString()), MaxSize: 4096}}
	conf := NewLogConfig("info", DefaultLogFormat, "", "", fileCfg, false)
	err := InitLogger(conf)
	require.NoError(t, err)
	connID := uint64(123)
	ctx := WithConnID(context.Background(), connID)
	testZapLogger(ctx, t, fileCfg.Filename, zapLogWithConnIDPattern)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)

	conf = NewLogConfig("info", DefaultLogFormat, "", "", fileCfg, false)
	err = InitLogger(conf)
	require.NoError(t, err)
	ctx = WithConnID(context.Background(), connID)
	ctx = WithSessionAlias(ctx, "alias123")
	testZapLogger(ctx, t, fileCfg.Filename, zapLogWithTraceInfoPattern)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)

	err = InitLogger(conf)
	require.NoError(t, err)
	ctx1 := WithFields(context.Background(), zap.Int64("conn", 123), zap.String("session_alias", "alias456"))
	testZapLogger(ctx1, t, fileCfg.Filename, zapLogWithTraceInfoPattern)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)

	err = InitLogger(conf)
	require.NoError(t, err)
	ctx1 = WithTraceFields(context.Background(), &tracing.TraceInfo{ConnectionID: 456, SessionAlias: "alias789"})
	testZapLogger(ctx1, t, fileCfg.Filename, zapLogWithTraceInfoPattern)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)

	err = InitLogger(conf)
	require.NoError(t, err)
	newLogger := LoggerWithTraceInfo(log.L(), &tracing.TraceInfo{ConnectionID: 789, SessionAlias: "alias012"})
	ctx1 = context.WithValue(context.Background(), CtxLogKey, newLogger)
	testZapLogger(ctx1, t, fileCfg.Filename, zapLogWithTraceInfoPattern)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)

	err = InitLogger(conf)
	require.NoError(t, err)
	newLogger = LoggerWithTraceInfo(log.L(), nil)
	ctx1 = context.WithValue(context.Background(), CtxLogKey, newLogger)
	testZapLogger(ctx1, t, fileCfg.Filename, zapLogWithoutCheckKeyPattern)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)

	err = InitLogger(conf)
	require.NoError(t, err)
	key := "ctxKey"
	val := "ctxValue"
	ctx1 = WithKeyValue(context.Background(), key, val)
	testZapLogger(ctx1, t, fileCfg.Filename, zapLogWithKeyValPatternByCtx)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)
}

func TestZapLoggerWithCore(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Skip this test on windows for two reason:
		// 1. The pattern match fails somehow. It seems windows treat \n as slash and character n.
		// 2. Remove file doesn't work as long as the log instance hold the file.
		t.Skip("skip on windows")
	}

	fileCfg := FileLogConfig{log.FileLogConfig{Filename: "zap_log", MaxSize: 4096}}
	conf := NewLogConfig("info", DefaultLogFormat, "", "", fileCfg, false)

	opt := zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return core.With([]zap.Field{zap.String("coreKey", "coreValue")})
	})

	err := InitLogger(conf, opt)
	require.NoError(t, err)
	testZapLogger(context.Background(), t, fileCfg.Filename, zapLogWithKeyValPatternByCore)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)
}

func testZapLogger(ctx context.Context, t *testing.T, fileName, pattern string) {
	Logger(ctx).Debug("debug msg", zap.String("test with key", "true"))
	Logger(ctx).Info("info msg", zap.String("test with key", "true"))
	Logger(ctx).Warn("warn msg", zap.String("test with key", "true"))
	Logger(ctx).Error("error msg", zap.String("test with key", "true"))

	f, err := os.Open(fileName)
	require.NoError(t, err)
	defer func() {
		err = f.Close()
		require.NoError(t, err)
	}()

	r := bufio.NewReader(f)
	for {
		var str string
		str, err = r.ReadString('\n')
		if err != nil {
			break
		}
		require.Regexp(t, pattern, str)
		require.NotContains(t, str, "stack")
		require.NotContains(t, str, "errorVerbose")
	}
	require.Equal(t, io.EOF, err)
}

func TestSetLevel(t *testing.T) {
	conf := NewLogConfig("info", DefaultLogFormat, "", "", EmptyFileLogConfig, false)
	err := InitLogger(conf)
	require.NoError(t, err)
	require.Equal(t, zap.InfoLevel, log.GetLevel())

	err = SetLevel("warn")
	require.NoError(t, err)
	require.Equal(t, zap.WarnLevel, log.GetLevel())
	err = SetLevel("Error")
	require.NoError(t, err)
	require.Equal(t, zap.ErrorLevel, log.GetLevel())
	err = SetLevel("DEBUG")
	require.NoError(t, err)
	require.Equal(t, zap.DebugLevel, log.GetLevel())
}

func TestSlowQueryLoggerAndGeneralLoggerCreation(t *testing.T) {
	var err error
	var l *zap.Logger
	for i := range 2 {
		level := "Error"
		conf := NewLogConfig(level, DefaultLogFormat, "", "", EmptyFileLogConfig, false)
		if i == 0 {
			l, _, err = newSlowQueryLogger(conf)
		} else {
			l, _, err = newGeneralLogger(conf)
		}
		// assert after init logger, the original conf is not changed
		require.Equal(t, conf.Level, level)
		require.NoError(t, err)
		// logger doesn't use the level of the global log config, and the
		// level should be equals to InfoLevel.
		require.NotEqual(t, conf.Level, l.Level().String())
		require.True(t, l.Level() == zapcore.InfoLevel)

		level = "warn"
		name := "test.log"
		fileConf := FileLogConfig{
			log.FileLogConfig{
				Filename:   name,
				MaxSize:    10,
				MaxDays:    10,
				MaxBackups: 10,
			},
		}
		conf = NewLogConfig(level, DefaultLogFormat, name, "", fileConf, false)
		if i == 0 {
			slowQueryConf := newSlowQueryLogConfig(conf)
			// slowQueryConf.MaxDays/MaxSize/MaxBackups should be same with global config.
			require.Equal(t, fileConf.FileLogConfig, slowQueryConf.File)
		} else {
			generalConf := newGeneralLogConfig(conf)
			// generalConf.MaxDays/MaxSize/MaxBackups should be same with global config.
			require.Equal(t, fileConf.FileLogConfig, generalConf.File)
		}
	}
}

func getWriteSyncerViaReflection(core *log.TextIOCore) (*zapcore.WriteSyncer, error) {
	val := reflect.ValueOf(core).Elem()

	outField := val.FieldByName("out")
	if !outField.IsValid() {
		return nil, fmt.Errorf("field 'out' not found in TextIOCore")
	}

	var out *zapcore.WriteSyncer
	if outField.Type().AssignableTo(reflect.TypeOf((*zapcore.WriteSyncer)(nil)).Elem()) {
		out = (*zapcore.WriteSyncer)(unsafe.Pointer(outField.UnsafeAddr()))
	}

	return out, nil
}

func TestSlowQueryLoggerAndGeneralUseSameLogFileName(t *testing.T) {
	fileName := t.TempDir() + "a.log"
	fileConf := FileLogConfig{
		log.FileLogConfig{
			Filename:   fileName,
			MaxSize:    10,
			MaxDays:    10,
			MaxBackups: 10,
		},
	}
	level := "info"
	// Use same log file for all logs
	conf := NewLogConfig(level, DefaultLogFormat, "", "", fileConf, false)
	require.NoError(t, InitLogger(conf))

	SlowQueryLogger.Info("123")
	GeneralLogger.Info("GENERAL LOG", zap.Int("test", 123))

	slowLogSyncer, err := getWriteSyncerViaReflection(SlowQueryLogger.Core().(*log.TextIOCore))
	require.NoError(t, err)
	require.NotNil(t, slowLogSyncer)

	generalLogSyncer, err := getWriteSyncerViaReflection(GeneralLogger.Core().(*log.TextIOCore))
	require.NoError(t, err)
	require.NotNil(t, generalLogSyncer)

	// use the same `WriteSyncer`
	require.Equal(t, generalLogSyncer, slowLogSyncer)

	f, err := os.Open(fileName)
	require.NoError(t, err)

	b := make([]byte, 1024)
	n, err := f.Read(b)
	require.NoError(t, err)
	// contains slow log and general log.
	require.True(t, strings.Contains(string(b[:n]), "# Time"))
	require.True(t, strings.Contains(string(b[:n]), "GENERAL LOG"))
}

func TestCompressedLog(t *testing.T) {
	level := "warn"
	fileConf := FileLogConfig{
		log.FileLogConfig{
			Filename:    "test.log",
			MaxSize:     10,
			MaxDays:     10,
			MaxBackups:  10,
			Compression: "xxx",
		},
	}
	conf := NewLogConfig(level, DefaultLogFormat, "test.log", "", fileConf, false)
	err := InitLogger(conf)
	require.Error(t, err)

	fileConf = FileLogConfig{
		log.FileLogConfig{
			Filename:    "test.log",
			MaxSize:     10,
			MaxDays:     10,
			MaxBackups:  10,
			Compression: "gzip",
		},
	}
	conf = NewLogConfig(level, DefaultLogFormat, "test.log", "", fileConf, false)
	err = InitLogger(conf)
	require.NoError(t, err)
}

func TestGlobalLoggerReplace(t *testing.T) {
	fileCfg := FileLogConfig{log.FileLogConfig{Filename: "zap_log", MaxDays: 0, MaxSize: 4096}}
	conf := NewLogConfig("info", DefaultLogFormat, "", "", fileCfg, false)
	err := InitLogger(conf)
	require.NoError(t, err)

	conf.Config.File.MaxDays = 14

	err = ReplaceLogger(conf)
	require.NoError(t, err)
	err = os.Remove(fileCfg.Filename)
	require.NoError(t, err)
}

func TestProxyFields(t *testing.T) {
	revIndex := map[string]int{
		"http_proxy":  0,
		"https_proxy": 1,
		"no_proxy":    2,
	}
	envs := [...]string{"http_proxy", "https_proxy", "no_proxy"}
	envPreset := [...]string{"http://127.0.0.1:8080", "https://127.0.0.1:8443", "localhost,127.0.0.1"}

	// Exhaust all combinations of those environment variables' selection.
	// Each bit of the mask decided whether this index of `envs` would be set.
	for mask := 0; mask <= 0b111; mask++ {
		for _, env := range envs {
			require.NoError(t, os.Unsetenv(env))
		}

		for i := range 3 {
			if (1<<i)&mask != 0 {
				require.NoError(t, os.Setenv(envs[i], envPreset[i]))
			}
		}

		for _, field := range proxyFields() {
			idx, ok := revIndex[field.Key]
			require.True(t, ok)
			require.NotZero(t, (1<<idx)&mask)
			require.Equal(t, envPreset[idx], field.String)
		}
	}
}

func prepareStdoutLogger(t *testing.T) (*os.File, string) {
	bak := os.Stdout
	t.Cleanup(func() {
		os.Stdout = bak
	})
	tempDir := t.TempDir()
	fileName := path.Join(tempDir, "test.log")
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	os.Stdout = file
	// InitLogger contains zap.AddStacktrace(zapcore.FatalLevel), so log level
	// below fatal will not contain stack automatically.
	require.NoError(t, InitLogger(&LogConfig{}))

	return file, fileName
}

func TestSampleLoggerFactory(t *testing.T) {
	file, filename := prepareStdoutLogger(t)
	fac := SampleLoggerFactory(time.Minute, 3, zap.String(LogFieldCategory, "ddl"))
	for range 100 {
		fac().Info("sample log test")
	}
	require.NoError(t, file.Close())
	content, err := os.ReadFile(filename)
	require.NoError(t, err)
	require.Equal(t, 3, strings.Count(string(content), "sample log test"))
}
