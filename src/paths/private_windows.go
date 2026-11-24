//go:build windows

package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ensurePrivateTempParent(parent string) error {
	info, err := os.Lstat(parent)
	if os.IsNotExist(err) {
		if err := createPrivateWindowsDirectory(parent); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("temporary parent is not a real directory")
		}
		if err := setPrivateWindowsACL(parent, true); err != nil {
			return err
		}
	}
	return verifyPrivateWindowsACL(parent, true)
}

func currentWindowsUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("identify temporary parent owner: %w", err)
	}
	return user.User.Sid, nil
}

func privateWindowsDescriptor(sid *windows.SID, directory bool) (*windows.SECURITY_DESCRIPTOR, *windows.SECURITY_DESCRIPTOR, error) {
	sidText := sid.String()
	inheritance := ""
	if directory {
		inheritance = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:" + sidText + "D:P(A;" + inheritance + ";FA;;;" + sidText + ")")
	if err != nil {
		return nil, nil, err
	}
	absolute, err := descriptor.ToAbsolute()
	if err != nil {
		return nil, nil, err
	}
	return descriptor, absolute, nil
}

func createPrivateWindowsDirectory(path string) error {
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		return fmt.Errorf("temporary directory parent is unavailable")
	}
	sid, err := currentWindowsUserSID()
	if err != nil {
		return err
	}
	_, descriptor, err := privateWindowsDescriptor(sid, true)
	if err != nil {
		return fmt.Errorf("create private temporary directory descriptor: %w", err)
	}
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	createErr := windows.CreateDirectory(pathUTF16, attributes)
	if createErr != nil && createErr != windows.ERROR_ALREADY_EXISTS {
		return fmt.Errorf("create private temporary directory: %w", createErr)
	}
	if createErr == windows.ERROR_ALREADY_EXISTS {
		return setPrivateWindowsACL(path, true)
	}
	return nil
}

func setPrivateWindowsACL(path string, directory bool) error {
	sid, err := currentWindowsUserSID()
	if err != nil {
		return err
	}
	current, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil || current == nil {
		return fmt.Errorf("verify temporary directory owner: %w", err)
	}
	currentOwner, _, err := current.Owner()
	if err != nil || currentOwner == nil || !currentOwner.Equals(sid) {
		return fmt.Errorf("temporary directory is not owned by the current user")
	}
	descriptor, _, err := privateWindowsDescriptor(sid, directory)
	if err != nil {
		return fmt.Errorf("create private temporary directory descriptor: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private temporary directory ACL: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read private temporary directory owner: %w", err)
	}
	security := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, security, owner, nil, dacl, nil); err != nil {
		return fmt.Errorf("secure temporary directory ACL: %w", err)
	}
	return verifyPrivateWindowsACL(path, directory)
}

func enforcePrivateMode(path string, mode os.FileMode) error {
	return setPrivateWindowsACL(path, mode.IsDir())
}

func verifyPrivateMode(path string, mode os.FileMode) error {
	return verifyPrivateWindowsACL(path, mode.IsDir())
}

func verifyPrivateWindowsACL(path string, directory bool) error {
	sid, err := currentWindowsUserSID()
	if err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("verify temporary directory ACL: %w", err)
	}
	if descriptor == nil {
		return fmt.Errorf("temporary directory has no security descriptor")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(sid) {
		return fmt.Errorf("temporary directory is not owned by the current user")
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("read temporary directory ACL protection: %w", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("temporary directory ACL is not protected")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		return fmt.Errorf("temporary directory ACL is not owner-only")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	wantFlags := uint8(0)
	if directory {
		wantFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x001f01ff || ace.Header.AceFlags != wantFlags {
		return fmt.Errorf("temporary directory ACL is not owner-only")
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.Equals(sid) {
		return fmt.Errorf("temporary directory ACL grants access to another principal")
	}
	return nil
}
