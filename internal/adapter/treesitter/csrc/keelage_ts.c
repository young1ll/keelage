// keelage_ts.c — the C side of the anchor resolver: tree-sitter runtime +
// the TypeScript/TSX grammars, compiled together into one wasm32-wasi
// module (no dynamic linking) and driven from Go through wazero.
//
// The exported surface is deliberately tiny and data-oriented: Go passes a
// source buffer in, gets a flat preorder dump of the tree (and of query
// matches) out, and does the rest in Go. No callbacks cross the boundary.
//
// Record layouts (all little-endian uint32):
//   tree dump:  depth, symbol, field_id, start_byte, end_byte, flags
//   match dump: one header record per match  0xFFFFFFFF, pattern_index, capture_count, 0, 0
//               then one record per capture   pattern_index, capture_index, symbol, start_byte, end_byte
// flags: bit0 named, bit1 missing, bit2 extra, bit3 has_error, bit4 is_error.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "tree_sitter/api.h"

#define EXPORT(name) __attribute__((export_name(name)))

const TSLanguage *tree_sitter_typescript(void);
const TSLanguage *tree_sitter_tsx(void);

enum { LANG_TYPESCRIPT = 0, LANG_TSX = 1, LANG_COUNT = 2 };

static const TSLanguage *lang_for(uint32_t id) {
  switch (id) {
    case LANG_TYPESCRIPT: return tree_sitter_typescript();
    case LANG_TSX: return tree_sitter_tsx();
    default: return NULL;
  }
}

// ---- memory ----

EXPORT("ks_malloc") void *ks_malloc(uint32_t n) { return malloc(n ? n : 1); }
EXPORT("ks_free") void ks_free(void *p) { free(p); }

// ---- language metadata ----

EXPORT("ks_lang_count") uint32_t ks_lang_count(void) { return LANG_COUNT; }

EXPORT("ks_lang_abi") uint32_t ks_lang_abi(uint32_t lang) {
  const TSLanguage *l = lang_for(lang);
  return l ? ts_language_abi_version(l) : 0;
}

EXPORT("ks_lang_symbol_count") uint32_t ks_lang_symbol_count(uint32_t lang) {
  const TSLanguage *l = lang_for(lang);
  return l ? ts_language_symbol_count(l) : 0;
}

EXPORT("ks_lang_symbol_name") const char *ks_lang_symbol_name(uint32_t lang, uint32_t sym) {
  const TSLanguage *l = lang_for(lang);
  return l ? ts_language_symbol_name(l, (TSSymbol)sym) : NULL;
}

EXPORT("ks_lang_symbol_named") uint32_t ks_lang_symbol_named(uint32_t lang, uint32_t sym) {
  const TSLanguage *l = lang_for(lang);
  return l ? ts_language_symbol_type(l, (TSSymbol)sym) == TSSymbolTypeRegular : 0;
}

EXPORT("ks_lang_field_count") uint32_t ks_lang_field_count(uint32_t lang) {
  const TSLanguage *l = lang_for(lang);
  return l ? ts_language_field_count(l) : 0;
}

EXPORT("ks_lang_field_name") const char *ks_lang_field_name(uint32_t lang, uint32_t id) {
  const TSLanguage *l = lang_for(lang);
  return l ? ts_language_field_name_for_id(l, (TSFieldId)id) : NULL;
}

// ---- parsing ----

EXPORT("ks_parser_new") TSParser *ks_parser_new(uint32_t lang) {
  const TSLanguage *l = lang_for(lang);
  if (!l) return NULL;
  TSParser *p = ts_parser_new();
  if (!ts_parser_set_language(p, l)) {
    ts_parser_delete(p);
    return NULL;
  }
  return p;
}

EXPORT("ks_parser_delete") void ks_parser_delete(TSParser *p) { ts_parser_delete(p); }

EXPORT("ks_parse") TSTree *ks_parse(TSParser *p, const char *src, uint32_t len) {
  ts_parser_reset(p);
  return ts_parser_parse_string(p, NULL, src, len);
}

EXPORT("ks_tree_delete") void ks_tree_delete(TSTree *t) { ts_tree_delete(t); }

// ---- tree dump ----

typedef struct {
  uint32_t *data;
  uint32_t len, cap;
} u32buf;

static int push(u32buf *b, uint32_t v) {
  if (b->len == b->cap) {
    uint32_t ncap = b->cap ? b->cap * 2 : 4096;
    uint32_t *nd = realloc(b->data, ncap * sizeof(uint32_t));
    if (!nd) return 0;
    b->data = nd;
    b->cap = ncap;
  }
  b->data[b->len++] = v;
  return 1;
}

static uint32_t node_flags(TSNode n) {
  uint32_t f = 0;
  if (ts_node_is_named(n)) f |= 1u;
  if (ts_node_is_missing(n)) f |= 2u;
  if (ts_node_is_extra(n)) f |= 4u;
  if (ts_node_has_error(n)) f |= 8u;
  if (ts_node_is_error(n)) f |= 16u;
  return f;
}

// ks_tree_dump walks the tree in preorder with a cursor (so field ids are
// available) and writes records into a malloc'd buffer. Returns the record
// count; *out receives the buffer (free with ks_free).
EXPORT("ks_tree_dump") uint32_t ks_tree_dump(TSTree *t, uint32_t **out) {
  *out = NULL;
  if (!t) return 0;
  u32buf b = {0};
  TSTreeCursor c = ts_tree_cursor_new(ts_tree_root_node(t));
  uint32_t depth = 0, count = 0;
  for (;;) {
    TSNode n = ts_tree_cursor_current_node(&c);
    if (!push(&b, depth) || !push(&b, ts_node_symbol(n)) || !push(&b, ts_tree_cursor_current_field_id(&c)) ||
        !push(&b, ts_node_start_byte(n)) || !push(&b, ts_node_end_byte(n)) || !push(&b, node_flags(n))) {
      free(b.data);
      ts_tree_cursor_delete(&c);
      return 0;
    }
    count++;
    if (ts_tree_cursor_goto_first_child(&c)) { depth++; continue; }
    for (;;) {
      if (ts_tree_cursor_goto_next_sibling(&c)) break;
      if (!ts_tree_cursor_goto_parent(&c)) {
        ts_tree_cursor_delete(&c);
        *out = b.data;
        return count;
      }
      depth--;
    }
  }
}

// ---- queries ----

EXPORT("ks_query_new") TSQuery *ks_query_new(uint32_t lang, const char *src, uint32_t len, uint32_t *err_offset, uint32_t *err_type) {
  const TSLanguage *l = lang_for(lang);
  if (!l) return NULL;
  TSQueryError e = TSQueryErrorNone;
  TSQuery *q = ts_query_new(l, src, len, err_offset, &e);
  *err_type = (uint32_t)e;
  return q;
}

EXPORT("ks_query_delete") void ks_query_delete(TSQuery *q) { ts_query_delete(q); }

EXPORT("ks_query_capture_count") uint32_t ks_query_capture_count(TSQuery *q) { return ts_query_capture_count(q); }

EXPORT("ks_query_capture_name") const char *ks_query_capture_name(TSQuery *q, uint32_t i, uint32_t *len) {
  return ts_query_capture_name_for_id(q, i, len);
}

// ks_query_exec runs q over the whole tree and dumps every capture of every
// match. Returns the record count; *out receives the buffer (free with ks_free).
EXPORT("ks_query_exec") uint32_t ks_query_exec(TSQuery *q, TSTree *t, uint32_t **out) {
  *out = NULL;
  if (!q || !t) return 0;
  u32buf b = {0};
  TSQueryCursor *cur = ts_query_cursor_new();
  ts_query_cursor_exec(cur, q, ts_tree_root_node(t));
  TSQueryMatch m;
  uint32_t count = 0;
  while (ts_query_cursor_next_match(cur, &m)) {
    if (!push(&b, 0xFFFFFFFFu) || !push(&b, m.pattern_index) || !push(&b, m.capture_count) || !push(&b, 0) || !push(&b, 0)) {
      free(b.data);
      ts_query_cursor_delete(cur);
      return 0;
    }
    count++;
    for (uint16_t i = 0; i < m.capture_count; i++) {
      TSNode n = m.captures[i].node;
      if (!push(&b, m.pattern_index) || !push(&b, m.captures[i].index) || !push(&b, ts_node_symbol(n)) ||
          !push(&b, ts_node_start_byte(n)) || !push(&b, ts_node_end_byte(n))) {
        free(b.data);
        ts_query_cursor_delete(cur);
        return 0;
      }
      count++;
    }
  }
  ts_query_cursor_delete(cur);
  *out = b.data;
  return count;
}
