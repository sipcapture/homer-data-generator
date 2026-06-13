package schema

// CallTable is hep_proto_1_call — must match homer-core ducklake TableSchema.
const CallTable = "hep_proto_1_call"

// CallCreateSQL is the DuckDB column layout for SIP call rows.
const CallCreateSQL = `
	uuid VARCHAR,
	date DATE,
	timestamp TIMESTAMP,
	session_id VARCHAR,
	caller VARCHAR,
	callee VARCHAR,
	src_ip VARCHAR,
	dst_ip VARCHAR,
	src_port UINTEGER,
	dst_port UINTEGER,
	method VARCHAR,
	response_code VARCHAR,
	cseq_method VARCHAR,
	protocol UINTEGER,
	node_id VARCHAR,
	cid VARCHAR,
	payload VARCHAR,
	data_extra JSON
`
