#!/bin/bash

setUp() {
  rm test*.xml 2>/dev/null || true
}

testXmlCDataEscapedByDefault() {
  cat >test.xml <<'EOL'
<root><note><![CDATA[<x>]]></note></root>
EOL

  read -r -d '' expected << EOM
<root>
  <note>&lt;x&gt;</note>
</root>
EOM

  X=$(./yq -p=xml -o=xml test.xml)
  assertEquals "$expected" "$X"

  X=$(./yq ea -p=xml -o=xml test.xml)
  assertEquals "$expected" "$X"
}

testXmlKeepCDataPureSection() {
  cat >test.xml <<'EOL'
<root>
  <note><![CDATA[<hello & world>]]></note>
</root>
EOL

  read -r -d '' expected << EOM
<root>
  <note><![CDATA[<hello & world>]]></note>
</root>
EOL

  X=$(./yq --xml-keep-cdata -p=xml -o=xml test.xml)
  assertEquals "$expected" "$X"

  X=$(./yq ea --xml-keep-cdata -p=xml -o=xml test.xml)
  assertEquals "$expected" "$X"
}

testXmlKeepCDataMixedSegments() {
  cat >test.xml <<'EOL'
<root><a>x<![CDATA[y&<z]]>w</a></root>
EOL

  read -r -d '' expected << EOM
<root>
  <a>x<![CDATA[y&<z]]>w</a>
</root>
EOM

  X=$(./yq --xml-keep-cdata -p=xml -o=xml test.xml)
  assertEquals "$expected" "$X"
}

testXmlKeepCDataRewrittenFallsBackToEscapedText() {
  cat >test.xml <<'EOL'
<root><a><![CDATA[x&<y]]></a></root>
EOL

  read -r -d '' expected << EOM
<root>
  <a>new&amp;&lt;v&gt;</a>
</root>
EOL

  X=$(./yq --xml-keep-cdata -p=xml -o=xml '.root.a = "new&<v>"' test.xml)
  assertEquals "$expected" "$X"
}

testXmlKeepCDataLeavesNoTraceInYaml() {
  cat >test.xml <<'EOL'
<root><note><![CDATA[x&<]]></note></root>
EOL

  read -r -d '' expected << EOM
root:
  note: x&<
EOM

  X=$(./yq --xml-keep-cdata -p=xml -o=yaml test.xml)
  assertEquals "$expected" "$X"
}

source ./scripts/shunit2
