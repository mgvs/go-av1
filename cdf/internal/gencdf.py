#!/usr/bin/env python3
# Extract Default_*_Cdf tables from the AV1 spec markdown (10.additional.tables.md)
# and emit Go nested-slice variables. The spec stores CDFs in spec-form (cumulative,
# last real entry 32768, trailing adaptation counter), which matches package msac.
import re, sys

SPEC = sys.argv[1]
OUT = sys.argv[2]
NAMES = sys.argv[3:]  # e.g. Skip Partition_W64 ...

txt = open(SPEC).read()

def find_table(name):
    # find "Default_<name>_Cdf[ ... ] = {" then capture balanced braces
    pat = re.compile(r"Default_%s_Cdf\s*\[[^=]*\]\s*=\s*" % re.escape(name))
    m = pat.search(txt)
    if not m:
        raise SystemExit("not found: Default_%s_Cdf" % name)
    i = txt.index('{', m.end())
    depth = 0
    j = i
    while j < len(txt):
        if txt[j] == '{': depth += 1
        elif txt[j] == '}':
            depth -= 1
            if depth == 0:
                return txt[i:j+1]
        j += 1
    raise SystemExit("unbalanced braces for %s" % name)

def to_go(body):
    # tokenize numbers and braces, compute nesting depth, emit Go nested []uint16.
    # First, replace C identifiers (none expected in cdf data) -> all numbers.
    # Determine max depth.
    s = body
    # depth of nesting
    depth = 0; maxd = 0
    for ch in s:
        if ch == '{': depth += 1; maxd = max(maxd, depth)
        elif ch == '}': depth -= 1
    # Go element type prefix per level: maxd levels -> [][]...uint16
    # Build by transforming { -> typed{ and numbers via regex; simplest: recursive parse.
    pos = 0
    def parse():
        nonlocal pos
        # expects s[pos]=='{'
        assert s[pos] == '{'
        pos += 1
        items = []
        is_leaf = None
        while True:
            while pos < len(s) and s[pos] in ' \n\t,': pos += 1
            if s[pos] == '}':
                pos += 1
                return items, is_leaf
            if s[pos] == '{':
                sub, leaf = parse()
                items.append((sub, leaf, False))
                is_leaf = False
            else:
                # leaf token: integer or "A * B" product expression
                m = re.match(r'-?\d+(?:\s*\*\s*-?\d+)*', s[pos:])
                tok = m.group(0)
                val = 1
                for part in re.split(r'\s*\*\s*', tok):
                    val *= int(part)
                items.append(str(val))
                pos += m.end()
                if is_leaf is None: is_leaf = True
    tree, _ = parse()
    def emit(node, level):
        # node is list; determine if leaf (list of number strings)
        if node and isinstance(node[0], str):
            return '{' + ', '.join(node) + '}'
        # else list of (subitems, leaf, _)
        parts = []
        for it in node:
            sub = it[0]
            parts.append(emit(sub, level+1))
        return '{' + ', '.join(parts) + '}'
    typ = '[]' * maxd + 'uint16'
    return typ, emit(tree, 0)

lines = ["// Code generated from AV1 spec 10.additional.tables.md by gencdf.py. DO NOT EDIT.",
         "package cdf", ""]
for name in NAMES:
    body = find_table(name)
    typ, go = to_go(body)
    goname = "Default" + name.replace('_', '') + "Cdf"
    lines.append("var %s = %s%s" % (goname, typ, go))
    lines.append("")
open(OUT, "w").write("\n".join(lines))
print("wrote", OUT, "tables:", NAMES)
