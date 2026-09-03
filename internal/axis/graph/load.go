package graph

import (
	"context"
	"database/sql"
	"fmt"
)

// Load reads the whole index into memory.
//
// Real indexes are small — cobra is 910 nodes and 4246 edges — and every rule
// here needs random access across the graph, so a single read beats issuing a
// query per candidate symbol.
func Load(ctx context.Context, db *sql.DB) (*Graph, error) {
	g := &Graph{
		Nodes:    map[string]Node{},
		inbound:  map[string][]Edge{},
		outbound: map[string][]Edge{},
		byFile:   map[string][]string{},
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, kind, name, qualified_name, file_path, start_line, end_line,
		       COALESCE(signature, '')
		FROM nodes`)
	if err != nil {
		return nil, fmt.Errorf("read nodes: %w", err)
	}
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Name, &n.Qualified, &n.File,
			&n.StartLine, &n.EndLine, &n.Signature); err != nil {
			_ = rows.Close()
			return nil, err
		}
		g.Nodes[n.ID] = n
		g.byFile[n.File] = append(g.byFile[n.File], n.ID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	erows, err := db.QueryContext(ctx, `
		SELECT source, target, kind, COALESCE(line, 0) FROM edges`)
	if err != nil {
		return nil, fmt.Errorf("read edges: %w", err)
	}
	defer func() { _ = erows.Close() }()
	for erows.Next() {
		var e Edge
		if err := erows.Scan(&e.Source, &e.Target, &e.Kind, &e.Line); err != nil {
			return nil, err
		}
		g.Edges = append(g.Edges, e)
		g.inbound[e.Target] = append(g.inbound[e.Target], e)
		g.outbound[e.Source] = append(g.outbound[e.Source], e)
	}
	return g, erows.Err()
}

// FindFunc locates the indexed symbol for a source function. Methods are keyed
// "Recv::Name" in the index; plain functions by bare name.
//
// The line is used to disambiguate, because a package can hold several methods
// of the same name on different receivers and the index does not always
// separate them.
func (g *Graph) FindFunc(file, name, recv string, startLine int) (Node, bool) {
	var best Node
	found := false
	for _, id := range g.byFile[file] {
		n := g.Nodes[id]
		if !n.IsFunc() || n.Name != name {
			continue
		}
		if recv != "" && n.Recv() != recv {
			continue
		}
		// Prefer the declaration nearest the line we know about.
		if !found || abs(n.StartLine-startLine) < abs(best.StartLine-startLine) {
			best, found = n, true
		}
	}
	return best, found
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
