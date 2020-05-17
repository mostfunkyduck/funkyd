package main

// manages the version metadata
// https://stackoverflow.com/questions/11354518/application-auto-build-versioning

import "fmt"

var versionHostname, versionBranch, versionTag, versionDate, versionRevision string

type Version struct {
	Hostname, Branch, Tag, Date, Revision string
}

func (v Version) String() string {
	return fmt.Sprintf("%s-%s-%s-%s-%s", v.Hostname, v.Branch, v.Tag, v.Date, v.Revision)
}

func GetVersion() Version {
	return Version{
		Hostname: versionHostname,
		Branch:   versionBranch,
		Tag:      versionTag,
		Date:     versionDate,
		Revision: versionRevision,
	}
}
