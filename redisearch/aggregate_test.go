package redisearch

import (
	"bufio"
	"compress/bzip2"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
)

// Game struct which contains a Asin, a Description, a Title, a Price, and a list of categories
// a type and a list of social links

// {"asin": "0984529527", "description": null, "title": "Dark Age Apocalypse: Forcelists HC", "brand": "Dark Age Miniatures", "price": 31.23, "categories": ["Games", "PC", "Video Games"]}
type Game struct {
	Asin        string   `json:"asin"`
	Description string   `json:"description"`
	Title       string   `json:"title"`
	Brand       string   `json:"brand"`
	Price       float32  `json:"price"`
	Categories  []string `json:"categories"`
}

func init() {
	/* load test data */
	value, exists := os.LookupEnv("REDISEARCH_RDB_LOADED")
	requiresDatagen := true
	if exists && value != "" {
		requiresDatagen = false
	}
	if requiresDatagen {
		c := createClient("bench.ft.aggregate")

		sc := NewSchema(DefaultOptions).
			AddField(NewTextField("foo"))
		c.Drop(defaultCtx)
		if err := c.CreateIndex(context.Background(), sc); err != nil {
			log.Fatal(err)
		}
		ndocs := 10000
		docs := make([]Document, ndocs)
		for i := 0; i < ndocs; i++ {
			docs[i] = NewDocument(fmt.Sprintf("bench.ft.aggregate.doc%d", i), 1).Set("foo", "hello world")
		}

		if err := c.IndexOptions(defaultCtx, DefaultIndexingOptions, docs...); err != nil {
			log.Fatal(err)
		}
	}

}

func benchmarkAggregate(c *Client, q *AggregateQuery, b *testing.B) {
	for n := 0; n < b.N; n++ {
		c.Aggregate(defaultCtx, q)
	}
}

func benchmarkAggregateCursor(c *Client, q *AggregateQuery, b *testing.B) {
	for n := 0; n < b.N; n++ {
		c.Aggregate(defaultCtx, q)
		for q.CursorHasResults() {
			c.Aggregate(defaultCtx, q)
		}
	}
}

func BenchmarkAgg_1(b *testing.B) {
	c := createClient("bench.ft.aggregate")
	q := NewAggregateQuery().
		SetQuery(NewQuery("*"))
	b.ResetTimer()
	benchmarkAggregate(c, q, b)
}

func BenchmarkAggCursor_1(b *testing.B) {
	c := createClient("bench.ft.aggregate")
	q := NewAggregateQuery().
		SetQuery(NewQuery("*")).
		SetCursor(NewCursor())
	b.ResetTimer()
	benchmarkAggregateCursor(c, q, b)
}

func AddValues(c *Client) {
	// Open our jsonFile
	bzipfile := "../tests/games.json.bz2"

	f, err := os.OpenFile(bzipfile, 0, 0)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	// create a reader
	br := bufio.NewReader(f)
	// create a bzip2.reader, using the reader we just created
	cr := bzip2.NewReader(br)
	// create a reader, using the bzip2.reader we were passed
	d := bufio.NewReader(cr)
	// create a scanner
	scanner := bufio.NewScanner(d)
	docs := make([]Document, 0)
	docPos := 1
	for scanner.Scan() {
		// we initialize our Users array
		var game Game

		err := json.Unmarshal(scanner.Bytes(), &game)
		if err != nil {
			fmt.Println("error:", err)
		}
		docs = append(docs, NewDocument(fmt.Sprintf("docs-games-%d", docPos), 1).
			Set("title", game.Title).
			Set("brand", game.Brand).
			Set("description", game.Description).
			Set("price", game.Price).
			Set("categories", strings.Join(game.Categories, ",")))
		docPos = docPos + 1
	}

	if err := c.IndexOptions(defaultCtx, DefaultIndexingOptions, docs...); err != nil {
		log.Fatal(err)
	}

}

func _init() {
	/* load test data */
	c := createClient("docs-games-idx1")

	sc := NewSchema(DefaultOptions).
		AddField(NewTextFieldOptions("title", TextFieldOptions{Sortable: true})).
		AddField(NewTextFieldOptions("brand", TextFieldOptions{Sortable: true, NoStem: true})).
		AddField(NewTextField("description")).
		AddField(NewSortableNumericField("price")).
		AddField(NewTagField("categories"))

	c.Drop(defaultCtx)
	c.CreateIndex(context.Background(), sc)

	AddValues(c)
}

func TestAggregateSortByMax(t *testing.T) {
	_init()
	c := createClient("docs-games-idx1")

	q1 := NewAggregateQuery().SetQuery(NewQuery("sony")).
		SetMax(60).
		SortBy([]SortingKey{*NewSortingKeyDir("@price", false)})

	res, _, err := c.Aggregate(defaultCtx, q1)
	assert.Nil(t, err)
	f1, _ := strconv.ParseFloat(res[0][1], 64)
	f2, _ := strconv.ParseFloat(res[1][1], 64)
	assert.GreaterOrEqual(t, f1, f2)
	assert.Less(t, f1, 696.0)

	_, rep, err := c.AggregateQuery(defaultCtx, q1)
	assert.Nil(t, err)
	f1, _ = strconv.ParseFloat(rep[0]["price"].(string), 64)
	f2, _ = strconv.ParseFloat(rep[1]["price"].(string), 64)
	assert.GreaterOrEqual(t, f1, f2)
	assert.Less(t, f1, 696.0)
}

func TestAggregateGroupBy(t *testing.T) {
	_init()
	c := createClient("docs-games-idx1")

	q1 := NewAggregateQuery().
		GroupBy(*NewGroupBy().AddFields("@brand").
			Reduce(*NewReducerAlias("", nil, "count").
				SetName(GroupByReducerCount).
				SetArgs([]string{}))).
		SortBy([]SortingKey{*NewSortingKeyDir("@count", false)}).
		Limit(0, 5)

	_, count, err := c.Aggregate(defaultCtx, q1)
	assert.Nil(t, err)
	assert.Equal(t, 5, count)

	count, _, err = c.AggregateQuery(defaultCtx, q1)
	assert.Nil(t, err)
	assert.Equal(t, 5, count)
}

func TestAggregateMinMax(t *testing.T) {
	_init()
	c := createClient("docs-games-idx1")

	q1 := NewAggregateQuery().SetQuery(NewQuery("sony")).
		GroupBy(*NewGroupBy().AddFields("@brand").
			Reduce(*NewReducer(GroupByReducerCount, []string{})).
			Reduce(*NewReducerAlias(GroupByReducerMin, []string{"@price"}, "minPrice"))).
		SortBy([]SortingKey{*NewSortingKeyDir("@minPrice", false)})

	res, _, err := c.Aggregate(defaultCtx, q1)
	assert.Nil(t, err)
	row := res[0]
	fmt.Println(row)
	f, _ := strconv.ParseFloat(row[5], 64)
	assert.GreaterOrEqual(t, f, 88.0)
	assert.Less(t, f, 89.0)

	_, rep, err := c.AggregateQuery(defaultCtx, q1)
	assert.Nil(t, err)
	fmt.Println(rep[0])
	f, _ = strconv.ParseFloat(rep[0]["minPrice"].(string), 64)
	assert.GreaterOrEqual(t, f, 88.0)
	assert.Less(t, f, 89.0)

	q2 := NewAggregateQuery().SetQuery(NewQuery("sony")).
		GroupBy(*NewGroupBy().AddFields("@brand").
			Reduce(*NewReducer(GroupByReducerCount, []string{})).
			Reduce(*NewReducerAlias(GroupByReducerMax, []string{"@price"}, "maxPrice"))).
		SortBy([]SortingKey{*NewSortingKeyDir("@maxPrice", false)})

	res, _, err = c.Aggregate(defaultCtx, q2)
	assert.Nil(t, err)
	row = res[0]
	f, _ = strconv.ParseFloat(row[5], 64)
	assert.GreaterOrEqual(t, f, 695.0)
	assert.Less(t, f, 696.0)

	_, rep, err = c.AggregateQuery(defaultCtx, q2)
	assert.Nil(t, err)
	f, _ = strconv.ParseFloat(rep[0]["maxPrice"].(string), 64)
	assert.GreaterOrEqual(t, f, 695.0)
	assert.Less(t, f, 696.0)
}

func TestAggregateCountDistinct(t *testing.T) {
	_init()
	c := createClient("docs-games-idx1")

	q1 := NewAggregateQuery().
		GroupBy(*NewGroupBy().AddFields("@brand").
			Reduce(*NewReducer(GroupByReducerCountDistinct, []string{"@title"}).SetAlias("count_distinct(title)")).
			Reduce(*NewReducer(GroupByReducerCount, []string{})))

	res, _, err := c.Aggregate(defaultCtx, q1)
	assert.Nil(t, err)
	row := res[0]
	assert.Equal(t, "1484", row[3])

	_, rep, err := c.AggregateQuery(defaultCtx, q1)
	assert.Nil(t, err)
	assert.Equal(t, "1484", rep[0]["count_distinct(title)"])
}

func TestAggregateToList(t *testing.T) {
	_init()
	c := createClient("docs-games-idx1")

	q1 := NewAggregateQuery().
		GroupBy(*NewGroupBy().AddFields("@brand").
			Reduce(*NewReducer(GroupByReducerToList, []string{"@brand"})))

	total, reply, err := c.AggregateQuery(defaultCtx, q1) // Can't be used with Aggregate when using ToList!
	assert.Nil(t, err)
	assert.Equal(t, 292, total)
	_, ok := reply[0]["brand"].(string)
	assert.True(t, ok)
	_, ok = reply[0]["__generated_aliastolistbrand"].([]string)
	assert.True(t, ok)
}

func TestAggregateFilter(t *testing.T) {
	_init()
	c := createClient("docs-games-idx1")

	q1 := NewAggregateQuery().
		GroupBy(*NewGroupBy().AddFields("@brand").
			Reduce(*NewReducerAlias(GroupByReducerCount, []string{}, "count"))).
		Filter("@count > 5")

	res, _, err := c.Aggregate(defaultCtx, q1)
	assert.Nil(t, err)
	for _, row := range res {
		f, _ := strconv.ParseFloat(row[3], 64)
		assert.Greater(t, f, 5.0)
	}

	_, rep, err := c.AggregateQuery(defaultCtx, q1)
	assert.Nil(t, err)
	for _, row := range rep {
		f, _ := strconv.ParseFloat(row["count"].(string), 64)
		assert.Greater(t, f, 5.0)
	}
}

func TestAggregateApply(t *testing.T) {
	_init()
	c := createClient("docs-games-idx1")

	q1 := NewAggregateQuery().
		GroupBy(*NewGroupBy().AddFields("@brand").
			Reduce(*NewReducerAlias(GroupByReducerCount, []string{}, "count"))).
		Apply(*NewProjection("@count/2", "halfCount"))

	res, _, err := c.Aggregate(defaultCtx, q1)
	assert.Nil(t, err)
	count, _ := strconv.ParseFloat(res[0][3], 64)
	halfCount, _ := strconv.ParseFloat(res[0][5], 64)
	assert.Equal(t, halfCount*2, count)

	_, rep, err := c.AggregateQuery(defaultCtx, q1)
	assert.Nil(t, err)
	count, _ = strconv.ParseFloat(rep[0]["count"].(string), 64)
	halfCount, _ = strconv.ParseFloat(rep[0]["halfCount"].(string), 64)
	assert.Equal(t, halfCount*2, count)
}

func makeAggResponseInterface(seed int64, nElements int, responseSizes []int) (res []interface{}) {
	rand.Seed(seed)
	nInner := len(responseSizes)
	s := make([]interface{}, nElements)
	for i := 0; i < nElements; i++ {
		sIn := make([]interface{}, nInner)
		for pos, elementSize := range responseSizes {
			token := make([]byte, elementSize)
			rand.Read(token)
			sIn[pos] = string(token)
		}
		s[i] = sIn
	}
	return s
}

func benchmarkProcessAggResponseSS(res []interface{}, total int, b *testing.B) {
	for n := 0; n < b.N; n++ {
		ProcessAggResponseSS(res)
	}
}

func benchmarkProcessAggResponse(res []interface{}, total int, b *testing.B) {
	for n := 0; n < b.N; n++ {
		ProcessAggResponse(res)
	}
}

func BenchmarkProcessAggResponse_10x4Elements(b *testing.B) {
	res := makeAggResponseInterface(12345, 10, []int{4, 20, 20, 4})
	b.ResetTimer()
	benchmarkProcessAggResponse(res, 10, b)
}

func BenchmarkProcessAggResponseSS_10x4Elements(b *testing.B) {
	res := makeAggResponseInterface(12345, 10, []int{4, 20, 20, 4})
	b.ResetTimer()
	benchmarkProcessAggResponseSS(res, 10, b)
}

func BenchmarkProcessAggResponse_100x4Elements(b *testing.B) {
	res := makeAggResponseInterface(12345, 100, []int{4, 20, 20, 4})
	b.ResetTimer()
	benchmarkProcessAggResponse(res, 100, b)
}

func BenchmarkProcessAggResponseSS_100x4Elements(b *testing.B) {
	res := makeAggResponseInterface(12345, 100, []int{4, 20, 20, 4})
	b.ResetTimer()
	benchmarkProcessAggResponseSS(res, 100, b)
}

func TestProjection_Serialize(t *testing.T) {
	tests := []struct {
		name       string
		projection Projection
		want       redis.Args
	}{
		{"Test_Serialize_1", *NewProjection("sqrt(log(foo) * floor(@bar/baz)) + (3^@qaz % 6)", "sqrt"), redis.Args{"APPLY", "sqrt(log(foo) * floor(@bar/baz)) + (3^@qaz % 6)", "AS", "sqrt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.projection.Serialize(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("serialize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCursor_Serialize(t *testing.T) {
	tests := []struct {
		name   string
		cursor Cursor
		want   redis.Args
	}{
		{"TestCursor_Serialize_basic", *NewCursor().SetId(1), redis.Args{"WITHCURSOR"}},
		{"TestCursor_Serialize_MAXIDLE", *NewCursor().SetId(1).SetMaxIdle(30000), redis.Args{"WITHCURSOR", "MAXIDLE", 30000}},
		{"TestCursor_Serialize_COUNT_MAXIDLE", *NewCursor().SetId(1).SetMaxIdle(30000).SetCount(10), redis.Args{"WITHCURSOR", "COUNT", 10, "MAXIDLE", 30000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cursor.Serialize(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("serialize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQuery_Serialize(t *testing.T) {
	tests := []struct {
		name  string
		query AggregateQuery
		want  redis.Args
	}{
		{"TestQuery_Serialize_basic", *NewAggregateQuery(), redis.Args{"*"}},
		{"TestQuery_Serialize_WITHSCHEMA", *NewAggregateQuery().SetWithSchema(true), redis.Args{"*", "WITHSCHEMA"}},
		{"TestQuery_Serialize_VERBATIM", *NewAggregateQuery().SetVerbatim(true), redis.Args{"*", "VERBATIM"}},
		{"TestQuery_Serialize_WITHCURSOR", *NewAggregateQuery().SetCursor(NewCursor()), redis.Args{"*", "WITHCURSOR"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.query.Serialize(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("serialize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGroupBy_Serialize(t *testing.T) {
	testsSerialize := []struct {
		name  string
		group GroupBy
		want  redis.Args
	}{
		{"TestGroupBy_Serialize_basic", *NewGroupBy(), redis.Args{"GROUPBY", 0}},
		{"TestGroupBy_Serialize_FIELDS", *NewGroupBy().AddFields("a"), redis.Args{"GROUPBY", 1, "a"}},
		{"TestGroupBy_Serialize_FIELDS_2", *NewGroupBy().AddFields([]string{"a", "b"}), redis.Args{"GROUPBY", 2, "a", "b"}},
		{"TestGroupBy_Serialize_REDUCE", *NewGroupBy().Reduce(*NewReducerAlias(GroupByReducerCount, []string{}, "count")), redis.Args{"GROUPBY", 0, "REDUCE", "COUNT", 0, "AS", "count"}},
		{"TestGroupBy_Serialize_LIMIT", *NewGroupBy().Limit(10, 20), redis.Args{"GROUPBY", 0, "LIMIT", 10, 20}},
	}
	for _, tt := range testsSerialize {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.group.Serialize(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("serialize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGroupBy_AddFields(t *testing.T) {
	type fields struct {
		Fields   []string
		Reducers []Reducer
		Paging   *Paging
	}
	type args struct {
		fields interface{}
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *GroupBy
	}{
		{"TestGroupBy_AddFields_single_field",
			fields{[]string{}, nil, nil},
			args{"a"},
			&GroupBy{[]string{"a"}, nil, nil},
		},
		{"TestGroupBy_AddFields_multi_fields",
			fields{[]string{}, nil, nil},
			args{[]string{"a", "b", "c"}},
			&GroupBy{[]string{"a", "b", "c"}, nil, nil},
		},
		{"TestGroupBy_AddFields_nil",
			fields{[]string{}, nil, nil},
			args{nil},
			&GroupBy{[]string{}, nil, nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &GroupBy{
				Fields:   tt.fields.Fields,
				Reducers: tt.fields.Reducers,
				Paging:   tt.fields.Paging,
			}
			if got := g.AddFields(tt.args.fields); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AddFields() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGroupBy_Limit(t *testing.T) {
	type fields struct {
		Fields   []string
		Reducers []Reducer
		Paging   *Paging
	}
	type args struct {
		offset int
		num    int
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *GroupBy
	}{
		{"TestGroupBy_Limit_1",
			fields{[]string{}, nil, nil},
			args{10, 20},
			&GroupBy{[]string{}, nil, &Paging{10, 20}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &GroupBy{
				Fields:   tt.fields.Fields,
				Reducers: tt.fields.Reducers,
				Paging:   tt.fields.Paging,
			}
			if got := g.Limit(tt.args.offset, tt.args.num); (got.Paging.Num != tt.want.Paging.Num) || (got.Paging.Offset != tt.want.Paging.Offset) {
				t.Errorf("Limit() = %v, want %v, %v, want %v",
					got.Paging.Num, tt.want.Paging.Num,
					got.Paging.Offset, tt.want.Paging.Offset)
			}
		})
	}
}

func TestAggregateQuery_SetMax(t *testing.T) {
	type fields struct {
		Query         *Query
		AggregatePlan redis.Args
		Paging        *Paging
		Max           int
		WithSchema    bool
		Verbatim      bool
		WithCursor    bool
		Cursor        *Cursor
	}
	type args struct {
		value int
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *AggregateQuery
	}{
		{"TestAggregateQuery_SetMax_1",
			fields{nil, redis.Args{}, nil, 0, false, false, false, nil},
			args{10},
			&AggregateQuery{nil, redis.Args{}, nil, 10, false, false, false, nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AggregateQuery{
				Query:         tt.fields.Query,
				AggregatePlan: tt.fields.AggregatePlan,
				Paging:        tt.fields.Paging,
				Max:           tt.fields.Max,
				WithSchema:    tt.fields.WithSchema,
				Verbatim:      tt.fields.Verbatim,
				WithCursor:    tt.fields.WithCursor,
				Cursor:        tt.fields.Cursor,
			}
			if got := a.SetMax(tt.args.value); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SetMax() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAggregateQuery_SetVerbatim(t *testing.T) {
	type fields struct {
		Query         *Query
		AggregatePlan redis.Args
		Paging        *Paging
		Max           int
		WithSchema    bool
		Verbatim      bool
		WithCursor    bool
		Cursor        *Cursor
	}
	type args struct {
		value bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *AggregateQuery
	}{
		{"TestAggregateQuery_SetVerbatim_1",
			fields{nil, redis.Args{}, nil, 0, false, false, false, nil},
			args{true},
			&AggregateQuery{nil, redis.Args{}, nil, 0, false, true, false, nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AggregateQuery{
				Query:         tt.fields.Query,
				AggregatePlan: tt.fields.AggregatePlan,
				Paging:        tt.fields.Paging,
				Max:           tt.fields.Max,
				WithSchema:    tt.fields.WithSchema,
				Verbatim:      tt.fields.Verbatim,
				WithCursor:    tt.fields.WithCursor,
				Cursor:        tt.fields.Cursor,
			}
			if got := a.SetVerbatim(tt.args.value); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SetVerbatim() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAggregateQuery_SetWithSchema(t *testing.T) {
	type fields struct {
		Query         *Query
		AggregatePlan redis.Args
		Paging        *Paging
		Max           int
		WithSchema    bool
		Verbatim      bool
		WithCursor    bool
		Cursor        *Cursor
	}
	type args struct {
		value bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *AggregateQuery
	}{
		{"TestAggregateQuery_SetWithSchema_1",
			fields{nil, redis.Args{}, nil, 0, false, false, false, nil},
			args{true},
			&AggregateQuery{nil, redis.Args{}, nil, 0, true, false, false, nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AggregateQuery{
				Query:         tt.fields.Query,
				AggregatePlan: tt.fields.AggregatePlan,
				Paging:        tt.fields.Paging,
				Max:           tt.fields.Max,
				WithSchema:    tt.fields.WithSchema,
				Verbatim:      tt.fields.Verbatim,
				WithCursor:    tt.fields.WithCursor,
				Cursor:        tt.fields.Cursor,
			}
			if got := a.SetWithSchema(tt.args.value); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SetWithSchema() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAggregateQuery_CursorHasResults(t *testing.T) {
	type fields struct {
		Query         *Query
		AggregatePlan redis.Args
		Paging        *Paging
		Max           int
		WithSchema    bool
		Verbatim      bool
		WithCursor    bool
		Cursor        *Cursor
	}
	tests := []struct {
		name    string
		fields  fields
		wantRes bool
	}{
		{"TestAggregateQuery_CursorHasResults_1_false",
			fields{nil, redis.Args{}, nil, 0, false, false, false, nil},
			false,
		},
		{"TestAggregateQuery_CursorHasResults_1_true",
			fields{nil, redis.Args{}, nil, 0, false, false, false, NewCursor().SetId(10)},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AggregateQuery{
				Query:         tt.fields.Query,
				AggregatePlan: tt.fields.AggregatePlan,
				Paging:        tt.fields.Paging,
				Max:           tt.fields.Max,
				WithSchema:    tt.fields.WithSchema,
				Verbatim:      tt.fields.Verbatim,
				WithCursor:    tt.fields.WithCursor,
				Cursor:        tt.fields.Cursor,
			}
			if gotRes := a.CursorHasResults(); gotRes != tt.wantRes {
				t.Errorf("CursorHasResults() = %v, want %v", gotRes, tt.wantRes)
			}
		})
	}
}

func TestAggregateQuery_Load(t *testing.T) {
	type fields struct {
		Query         *Query
		AggregatePlan redis.Args
		Paging        *Paging
		Max           int
		WithSchema    bool
		Verbatim      bool
		WithCursor    bool
		Cursor        *Cursor
	}
	type args struct {
		Properties []string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   redis.Args
	}{
		{"TestAggregateQuery_Load_1",
			fields{nil, redis.Args{}, nil, 0, false, false, false, nil},
			args{[]string{"field1"}},
			redis.Args{"*", "LOAD", 1, "@field1"},
		},
		{"TestAggregateQuery_Load_2",
			fields{nil, redis.Args{}, nil, 0, false, false, false, nil},
			args{[]string{"field1", "field2", "field3", "field4"}},
			redis.Args{"*", "LOAD", 4, "@field1", "@field2", "@field3", "@field4"},
		},
		{"TestAggregateQuery_Load_All",
			fields{nil, redis.Args{}, nil, 0, false, false, false, nil},
			args{[]string{}},
			redis.Args{"*", "LOAD", "*"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AggregateQuery{
				Query:         tt.fields.Query,
				AggregatePlan: tt.fields.AggregatePlan,
				Paging:        tt.fields.Paging,
				Max:           tt.fields.Max,
				WithSchema:    tt.fields.WithSchema,
				Verbatim:      tt.fields.Verbatim,
				WithCursor:    tt.fields.WithCursor,
				Cursor:        tt.fields.Cursor,
			}
			if got := a.Load(tt.args.Properties).Serialize(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Load() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessAggResponse(t *testing.T) {
	type args struct {
		res []interface{}
	}
	tests := []struct {
		name string
		args args
		want [][]string
	}{
		{"empty-reply", args{[]interface{}{}}, [][]string{}},
		{"1-element-reply", args{[]interface{}{[]interface{}{"userFullName", "berge, julius", "count", "2783"}}}, [][]string{{"userFullName", "berge, julius", "count", "2783"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProcessAggResponse(tt.args.res); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ProcessAggResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_processAggReply(t *testing.T) {
	type args struct {
		res []interface{}
	}
	tests := []struct {
		name               string
		args               args
		wantTotal          int
		wantAggregateReply [][]string
		wantErr            bool
	}{
		{"empty-reply", args{[]interface{}{}}, 0, [][]string{}, false},
		{"1-element-reply", args{[]interface{}{1, []interface{}{"userFullName", "j", "count", "2"}}}, 1, [][]string{{"userFullName", "j", "count", "2"}}, false},
		{"multi-element-reply", args{[]interface{}{2, []interface{}{"userFullName", "j"}, []interface{}{"userFullName", "a"}}}, 2, [][]string{{"userFullName", "j"}, {"userFullName", "a"}}, false},
		{"invalid-parsing-type", args{[]interface{}{1, []interface{}{[]interface{}{nil, nil}}}}, 1, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTotal, gotAggregateReply, err := processAggReply(tt.args.res)
			if (err != nil) != tt.wantErr {
				t.Errorf("processAggReply() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotTotal != tt.wantTotal {
				t.Errorf("processAggReply() gotTotal = %v, want %v", gotTotal, tt.wantTotal)
			}
			if !tt.wantErr && !reflect.DeepEqual(gotAggregateReply, tt.wantAggregateReply) {
				t.Errorf("processAggReply() gotAggregateReply = %v, want %v", gotAggregateReply, tt.wantAggregateReply)
			}
		})
	}
}

func Test_processAggQueryReply(t *testing.T) {
	type args struct {
		res []interface{}
	}
	tests := []struct {
		name               string
		args               args
		wantTotal          int
		wantAggregateReply []map[string]interface{}
		wantErr            bool
	}{
		{"empty-reply", args{[]interface{}{}}, 0, []map[string]interface{}{}, false},
		{"1-element-reply", args{[]interface{}{1, []interface{}{"userFullName", "j", "count", "2"}}}, 1, []map[string]interface{}{{"userFullName": "j", "count": "2"}}, false},
		{"multi-element-reply", args{[]interface{}{2, []interface{}{"userFullName", "j"}, []interface{}{"userFullName", "a"}}}, 2, []map[string]interface{}{{"userFullName": "j"}, {"userFullName": "a"}}, false},
		{"odd-number-of-args", args{[]interface{}{1, []interface{}{"userFullName"}}}, 1, nil, true},
		{"invalid-key", args{[]interface{}{1, []interface{}{nil, "j"}}}, 1, nil, true},
		{"invalid-value", args{[]interface{}{1, []interface{}{"userFullName", fmt.Errorf("invalid value type")}}}, 1, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTotal, gotAggregateReply, err := processAggQueryReply(tt.args.res)
			if (err != nil) != tt.wantErr {
				t.Errorf("processAggQueryReply() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotTotal != tt.wantTotal {
				t.Errorf("processAggQueryReply() gotTotal = %v, want %v", gotTotal, tt.wantTotal)
			}
			if !tt.wantErr && !reflect.DeepEqual(gotAggregateReply, tt.wantAggregateReply) {
				t.Errorf("processAggQueryReply() gotAggregateReply = %v, want %v", gotAggregateReply, tt.wantAggregateReply)
			}
		})
	}
}
