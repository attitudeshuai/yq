#!/bin/bash

setUp() {
  rm -f test_source*.yml test_inner_missing.yml test_source_gone.yml
}

testSingleFileFirstDocumentFailureIsUnchanged() {
  cat >test_source.yml <<EOL
a: one
EOL

  result=$(./yq e 'error("nope")' test_source.yml 2>&1)
  assertEquals 1 $?
  assertEquals "Error: nope" "$result"
}

testFailureDoesNotWriteToStdout() {
  cat >test_source.yml <<EOL
a: one
EOL

  out=$(./yq e 'error("nope")' test_source.yml 2>/dev/null)
  assertEquals 1 $?
  assertEquals "" "$out"
}

testLaterDocumentFailureCarriesDocumentIndex() {
  cat >test_source.yml <<EOL
a: one
---
a: two
EOL

  result=$(./yq e 'if di == 1 then error("nope") else . end' test_source.yml 2>&1 1>/dev/null)
  assertEquals 1 $?
  assertEquals "Error: test_source.yml: document index 1: nope" "$result"
}

testMultiFileFailureCarriesFilename() {
  cat >test_source1.yml <<EOL
a: one
EOL
  cat >test_source2.yml <<EOL
a: two
EOL

  result=$(./yq e 'if fi == 1 then error("nope") else . end' test_source1.yml test_source2.yml 2>&1 1>/dev/null)
  assertEquals 1 $?
  assertEquals "Error: test_source2.yml: document index 0: nope" "$result"
}

testMissingInputIsBadFileFrame() {
  result=$(./yq e '.' test_source_gone.yml 2>&1)
  assertEquals 1 $?
  assertEquals "Error: bad file 'test_source_gone.yml': open test_source_gone.yml: no such file or directory" "$result"
}

testSyntaxErrorIsReportedBeforeReadingInputs() {
  result=$(./yq e '..(' test_source_gone.yml 2>&1)
  assertEquals 1 $?
  assertEquals "Error: bad expression - probably missing close bracket on RECURSIVE_DESCENT" "$result"
}

testLoadChainSingleFileShowsInnerOnly() {
  cat >test_source.yml <<EOL
a: one
EOL

  result=$(./yq e 'load("test_inner_missing.yml")' test_source.yml 2>&1)
  assertEquals 1 $?
  assertEquals "Error: failed to load test_inner_missing.yml: open test_inner_missing.yml: no such file or directory" "$result"
}

testLoadChainMultiFileShowsOuterAndInner() {
  cat >test_source1.yml <<EOL
a: one
EOL
  cat >test_source2.yml <<EOL
a: two
EOL

  result=$(./yq e 'if fi == 1 then load("test_inner_missing.yml") else . end' test_source1.yml test_source2.yml 2>&1 1>/dev/null)
  assertEquals 1 $?
  assertEquals "Error: test_source2.yml: document index 0: failed to load test_inner_missing.yml: open test_inner_missing.yml: no such file or directory" "$result"
}

testSplitNamingFailureSingleFileIsUnchanged() {
  cat >test_source.yml <<EOL
a: one
EOL

  result=$(./yq e -s 'error("nope")' test_source.yml 2>&1)
  assertEquals 1 $?
  assertEquals "Error: nope" "$result"
}

testSplitNamingFailureMultiFileCarriesFilename() {
  cat >test_source1.yml <<EOL
a: one
EOL
  cat >test_source2.yml <<EOL
a: two
EOL

  result=$(./yq e -s 'error("nope")' test_source1.yml test_source2.yml 2>&1 1>/dev/null)
  assertEquals 1 $?
  assertEquals "Error: test_source1.yml: document index 0: nope" "$result"
}

testWriteInPlaceMissingInputIsAttributed() {
  result=$(./yq e -i '.a' test_source_gone.yml 2>&1)
  assertEquals 1 $?
  assertEquals "Error: failed to write in place to 'test_source_gone.yml': stat test_source_gone.yml: no such file or directory" "$result"
}

testExitStatusNoMatchesUnchanged() {
  cat >test_source.yml <<EOL
a: one
EOL

  result=$(./yq e -e '.missing' test_source.yml 2>&1 1>/dev/null)
  assertEquals 1 $?
  assertEquals "Error: no matches found" "$result"
}

source ./scripts/shunit2
