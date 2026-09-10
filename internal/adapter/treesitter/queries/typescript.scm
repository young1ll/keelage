;; keelage symbols query for TypeScript / TSX.
;; Each pattern captures @symbol (the declaration), @name and, where the
;; declaration has a body, @body. The signature is the text before @body.

(function_declaration name: (identifier) @name body: (statement_block) @body) @symbol
(generator_function_declaration name: (identifier) @name body: (statement_block) @body) @symbol
(function_signature name: (identifier) @name) @symbol

(class_declaration name: (type_identifier) @name body: (class_body) @body) @symbol
(abstract_class_declaration name: (type_identifier) @name body: (class_body) @body) @symbol
(method_definition name: (property_identifier) @name body: (statement_block) @body) @symbol
(method_signature name: (property_identifier) @name) @symbol
(abstract_method_signature name: (property_identifier) @name) @symbol

(interface_declaration name: (type_identifier) @name body: (interface_body) @body) @symbol
(type_alias_declaration name: (type_identifier) @name value: (_) @body) @symbol
(enum_declaration name: (identifier) @name body: (enum_body) @body) @symbol
(internal_module name: (identifier) @name body: (statement_block) @body) @symbol

;; const f = (a) => ..., const g = function () {}
(lexical_declaration (variable_declarator name: (identifier) @name value: (arrow_function body: (_) @body))) @symbol
(lexical_declaration (variable_declarator name: (identifier) @name value: (function_expression body: (statement_block) @body))) @symbol
(variable_declaration (variable_declarator name: (identifier) @name value: (arrow_function body: (_) @body))) @symbol
(variable_declaration (variable_declarator name: (identifier) @name value: (function_expression body: (statement_block) @body))) @symbol
