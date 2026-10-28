//go:build windows

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestProtectFilesRemovesBroadWindowsGrants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitoring.db")
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.WriteFile(name, []byte("existing evidence"), 0600); err != nil {
			t.Fatal(err)
		}
		sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)")
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := protectFiles(path); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{path, path + "-wal", path + "-shm"} {
			sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkPrivateWindowsACL(sd, sid, false); err != nil {
				t.Fatalf("private DACL: %v; %s", err, sd.String())
			}
			if data, err := os.ReadFile(name); err != nil || string(data) != "existing evidence" {
				t.Fatalf("readback: %q %v", data, err)
			}
		}
	}
}

// GetAce returns a binary trustee SID; SDDL's LA/WD/BU aliases are presentation only.
func checkPrivateWindowsACL(sd *windows.SECURITY_DESCRIPTOR, expected *windows.SID, directory bool) error {
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("DACL is not protected: %v", err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return fmt.Errorf("missing or empty DACL: %v", err)
	}
	effective, children := false, false
	const fullControl = 0x1f01ff
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("unexpected ACE type %d", ace.Header.AceType)
		}
		trustee := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !trustee.Equals(expected) {
			return fmt.Errorf("unexpected trustee %s, want %s", trustee.String(), expected.String())
		}
		full := ace.Mask&windows.GENERIC_ALL != 0 || ace.Mask&fullControl == fullControl
		if !full {
			return fmt.Errorf("trustee lacks FullControl")
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 {
			effective = true
		}
		inherit := ace.Header.AceFlags & (windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
		if inherit == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			children = true
		}
		if !directory && ace.Header.AceFlags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE|windows.INHERIT_ONLY_ACE) != 0 {
			return fmt.Errorf("file has inheritance-only or propagating ACE")
		}
	}
	if !effective || directory && !children {
		return fmt.Errorf("missing effective or child-inheritance FullControl")
	}
	return nil
}

func TestPrivateFileACLTrusteeAliases(t *testing.T) {
	alias, err := windows.SecurityDescriptorFromString("D:PAI(A;;FA;;;LA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := alias.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	expected, err := windows.StringToSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)).String())
	if err != nil {
		t.Fatal(err)
	}
	for _, trustee := range []string{"LA", expected.String()} {
		sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + trustee + ")")
		if err != nil {
			t.Fatal(err)
		}
		if err := checkPrivateWindowsACL(sd, expected, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, trustee := range []string{"WD", "BU", "BA", "AU", "S-1-5-21-1-2-3-1002"} {
		sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + trustee + ")")
		if err != nil {
			t.Fatal(err)
		}
		if checkPrivateWindowsACL(sd, expected, false) == nil {
			t.Fatalf("unrelated trustee accepted: %s", trustee)
		}
	}
}
