package schemaversion

// Current is the exact application-wide database contract. Every API module
// and the explicit migrator must agree; additive modules do not pin old values.
const Current = 16
